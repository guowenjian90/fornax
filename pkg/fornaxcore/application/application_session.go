/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package application

import (
	"fmt"
	"reflect"
	"time"

	fornaxv1 "centaurusinfra.io/fornax-serverless/pkg/apis/core/v1"
	ie "centaurusinfra.io/fornax-serverless/pkg/fornaxcore/internal"
	"centaurusinfra.io/fornax-serverless/pkg/util"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

const (
	DefaultSessionPendingTimeoutDuration = 5 * time.Second
	DefaultSessionOpenTimeoutDuration    = 10 * time.Second
)

type ApplicationSessionSummary struct {
	pendingCount  int32
	startingCount int32
	idleCount     int32
	inUseCount    int32
	timeoutCount  int32
	closedCount   int32
	deletingCount int32
}

func (pool *ApplicationPool) sessionLength() int {
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	return len(pool.sessions)
}

func (pool *ApplicationPool) sessionList() []*fornaxv1.ApplicationSession {
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	keys := []*fornaxv1.ApplicationSession{}
	for _, v := range pool.sessions {
		keys = append(keys, v)
	}
	return keys
}

func (pool *ApplicationPool) summarySessions() ApplicationSessionSummary {
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	summary := ApplicationSessionSummary{}
	for _, v := range pool.sessionList() {
		if v.DeletionTimestamp == nil {
			if v.Status.SessionStatus == fornaxv1.SessionStatusUnspecified || v.Status.SessionStatus == fornaxv1.SessionStatusPending {
				summary.pendingCount += 1
			} else if v.Status.SessionStatus == fornaxv1.SessionStatusAvailable {
				summary.idleCount += 1
			} else if v.Status.SessionStatus == fornaxv1.SessionStatusStarting {
				summary.startingCount += 1
			} else if v.Status.SessionStatus == fornaxv1.SessionStatusOccupied {
				summary.inUseCount += 1
			} else if v.Status.SessionStatus == fornaxv1.SessionStatusClosed {
				summary.closedCount += 1
			} else if v.Status.SessionStatus == fornaxv1.SessionStatusTimeout {
				summary.timeoutCount += 1
			} else {
				summary.pendingCount += 1
			}
		} else {
			summary.deletingCount += 1
		}
	}
	return summary
}

func (pool *ApplicationPool) getSession(key string) *fornaxv1.ApplicationSession {
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	if v, found := pool.sessions[key]; found {
		return v
	}

	return nil
}

func (pool *ApplicationPool) addSession(key string, session *fornaxv1.ApplicationSession) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	pool.sessions[key] = session
}

func (pool *ApplicationPool) deleteSession(key string) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	delete(pool.sessions, key)
}

// groupSessionsByState return a list of session of different states,
// pending, not assigned to pod yet
// deleting, delete requested
// timeout, session timedout to get a pod, or session assigned to node, but timeout to get session state from node
// active, session assigned to pod, waiting for started by pod or being used or waiting for connection
func (pool *ApplicationPool) groupSessionsByState() (pendingSessions, deletingSessions, closingSessions, timeoutSessions, activeSessions []*fornaxv1.ApplicationSession) {
	for _, v := range pool.sessionList() {
		timeoutDuration := DefaultSessionOpenTimeoutDuration
		if v.Spec.OpenTimeoutSeconds > 0 {
			timeoutDuration = time.Duration(v.Spec.OpenTimeoutSeconds) * time.Second
		}
		pendingTimeoutTimeStamp := time.Now().Add(-1 * timeoutDuration)
		if v.Status.SessionStatus == fornaxv1.SessionStatusClosing {
			closingSessions = append(closingSessions, v)
		} else if v.DeletionTimestamp != nil {
			deletingSessions = append(deletingSessions, v)
		} else {
			if util.SessionIsPending(v) {
				if v.CreationTimestamp.Time.Before(pendingTimeoutTimeStamp) {
					timeoutSessions = append(timeoutSessions, v)
				} else {
					pendingSessions = append(pendingSessions, v)
				}
			} else if v.Status.SessionStatus == fornaxv1.SessionStatusStarting {
				if v.CreationTimestamp.Time.Before(pendingTimeoutTimeStamp) {
					timeoutSessions = append(timeoutSessions, v)
				} else {
					activeSessions = append(activeSessions, v)
				}
			} else if util.SessionIsOpen(v) {
				activeSessions = append(activeSessions, v)
			}
		}
	}
	return pendingSessions, deletingSessions, closingSessions, timeoutSessions, activeSessions
}

// treat node as authority for session status, session status from node could be Starting, Available, Closed,
// use status from node always, then session will be closed if session is already deleted
func (appc *ApplicationManager) onSessionEventFromNode(se *ie.SessionEvent) error {
	pod := se.Pod
	session := se.Session
	newStatus := session.Status.DeepCopy()
	applicationKey := session.Spec.ApplicationName
	pool := appc.getApplicationPool(applicationKey)
	oldCopy := pool.getSession(string(session.GetUID()))
	if oldCopy == nil {
		if util.SessionIsClosed(session) {
			appc.onApplicationSessionDeleteEvent(session)
		} else {
			appc.onApplicationSessionAddEvent(session)
		}
	} else {
		appc.onApplicationSessionUpdateEvent(oldCopy, session)
		// update session in go routine, this is persist status change to return by api server
		go func() {
			appc.sessionManager.UpdateSessionStatus(oldCopy.DeepCopy(), newStatus)
		}()

	}

	//termiante pod after sesion is closed
	if session.Spec.KillInstanceWhenSessionClosed && newStatus.SessionStatus == fornaxv1.SessionStatusClosed && util.PodNotTerminated(pod) {
		klog.InfoS("Terminate a pod as KillInstanceWhenSessionClosed is true", "pod", util.Name(pod), "session", util.Name(session))
		appc.podManager.TerminatePod(pod)
	}
	return nil
}

func (appc *ApplicationManager) changeSessionStatus(v *fornaxv1.ApplicationSession, status fornaxv1.SessionStatus) error {
	session := v.DeepCopy()
	newStatus := v.Status.DeepCopy()
	newStatus.SessionStatus = status
	if status == fornaxv1.SessionStatusClosed || status == fornaxv1.SessionStatusTimeout {
		// reset client session to let it can be hard deleted
		newStatus.ClientSessions = []v1.LocalObjectReference{}
	}
	return appc.sessionManager.UpdateSessionStatus(session, newStatus)
}

func (appc *ApplicationManager) getSessionApplicationKey(session *fornaxv1.ApplicationSession) (string, error) {
	applicationLabel := session.Spec.ApplicationName
	_, _, err := cache.SplitMetaNamespaceKey(applicationLabel)
	if err == nil {
		return applicationLabel, nil
	} else {
		return "", fmt.Errorf("Session application label:%s is not valid meta namespace key", applicationLabel)
	}
}

// add active session into application's session pool and delete terminal session from pool
func (appc *ApplicationManager) updateSessionPool(applicationKey string, session *fornaxv1.ApplicationSession, deleted bool) {
	sessionId := string(session.GetUID())
	pool := appc.getOrCreateApplicationPool(applicationKey)
	if util.SessionInTerminalState(session) {
		if session.Status.PodReference != nil {
			pod := pool.getPod(session.Status.PodReference.Name)
			if pod != nil {
				pod.sessions.Delete(sessionId)
			}
		}
		pool.deleteSession(sessionId)
	} else if deleted == false {
		if session.Status.PodReference != nil {
			podName := session.Status.PodReference.Name
			pod := pool.addPod(podName, NewApplicationPod(podName))
			pod.sessions.Add(sessionId)
		}
		pool.addSession(sessionId, session)
	} else {
		// delete event has high chance do not have session status updated when node have not reported back
		// update delete timestamp, sync application will close session when node report
		savedSession := pool.getSession(sessionId)
		if savedSession != nil && savedSession.DeletionTimestamp == nil {
			savedSession.DeletionTimestamp = util.NewCurrentMetaTime()
		}
	}
}

// callback from Application informer when ApplicationSession is created
// if session in terminal state, remove this session from pool(should not happen for a new session, but for weird case)
// else add new copy into pool
func (appc *ApplicationManager) onApplicationSessionAddEvent(obj interface{}) {
	session := obj.(*fornaxv1.ApplicationSession)
	if session.DeletionTimestamp != nil {
		appc.onApplicationSessionDeleteEvent(obj)
		return
	}
	sessionKey := util.Name(session)
	applicationKey, err := appc.getSessionApplicationKey(session)
	if err != nil {
		klog.ErrorS(err, "Can not get application key", "session", session)
		if util.SessionIsOpen(session) {
			appc.closeApplicationSession(session)
		}
	}
	klog.InfoS("Application session created", "session", sessionKey)
	appc.updateSessionPool(applicationKey, session, false)
	appc.enqueueApplication(applicationKey)
}

// callback from Application informer when ApplicationSession is updated
// if session in terminal state, remove this session from pool,
// else add new copy into pool
// do not need to sync application unless session is deleting or status change
func (appc *ApplicationManager) onApplicationSessionUpdateEvent(old, cur interface{}) {
	oldCopy := old.(*fornaxv1.ApplicationSession)
	newCopy := cur.(*fornaxv1.ApplicationSession)
	if reflect.DeepEqual(oldCopy, newCopy) {
		return
	}

	sessionKey := util.Name(newCopy)
	applicationKey, err := appc.getSessionApplicationKey(newCopy)
	if err != nil {
		klog.ErrorS(err, "Can not get application key", "session", newCopy)
		if util.SessionIsOpen(newCopy) {
			appc.closeApplicationSession(newCopy)
		}
	}

	pool := appc.getOrCreateApplicationPool(applicationKey)
	if v := pool.getSession(string(newCopy.GetUID())); v != nil {
		// use cached old copy in memory,
		oldCopy = v.DeepCopy()
	}

	appc.updateSessionPool(applicationKey, newCopy, false)
	if (newCopy.DeletionTimestamp != nil && oldCopy.DeletionTimestamp == nil) || !reflect.DeepEqual(oldCopy.Status, newCopy.Status) {
		klog.InfoS("Application session updated", "session", sessionKey, "status", newCopy.Status, "deleting", newCopy.DeletionTimestamp != nil)
		appc.enqueueApplication(applicationKey)
	}
}

// callback from Application informer when ApplicationSession is physically deleted
func (appc *ApplicationManager) onApplicationSessionDeleteEvent(obj interface{}) {
	session := obj.(*fornaxv1.ApplicationSession)
	sessionKey := util.Name(session)
	if session.DeletionTimestamp == nil {
		session.DeletionTimestamp = util.NewCurrentMetaTime()
	}

	applicationKey, err := appc.getSessionApplicationKey(session)
	if err != nil {
		klog.ErrorS(err, "Can not get application key", "session", session)
		return
	}

	klog.InfoS("Application session deleted", "session", sessionKey, "status", session.Status, "finalizer", session.Finalizers)
	appc.updateSessionPool(applicationKey, session, true)
	appc.enqueueApplication(applicationKey)
}

// return sum of of all in use, idle, pending session, and all pending sessions
func (appc *ApplicationManager) getTotalAndPendingSessionNum(applicationKey string) (int, int) {
	if pool := appc.getApplicationPool(applicationKey); pool != nil {
		summary := pool.summarySessions()
		return int(summary.idleCount + summary.inUseCount + summary.pendingCount + summary.startingCount + summary.deletingCount), int(summary.pendingCount)
	}
	return 0, 0
}

// syncApplicationSessions grab a list of pending session and try to allocate them to pods and call OpenSession on choosen pod.
// session status change in memory to SessionStatusStarting, but do not update etcd to avoid unnecessary resync.
// session status will be changed in etcd until pod report back, if fornax core restart and lost these memory state, it rely on pod to report back.
// It also cleanup session when a session is in Starting or Pending state for more than a timeout duration.
// session is changed to SessionStatusTimeout, session client need to create a new session.
// It also cleanup session in deletingSessions when a session is in Starting or Pending state for more than a timeout duration.
// session is changed to SessionStatusClosed, session client need to create a new session.
// session timedout and closed are removed from application pool's session list, so, syncApplicationPods do not need to consider these sessions anymore
func (appc *ApplicationManager) syncApplicationSessions(application *fornaxv1.Application, applicationKey string) error {
	pool := appc.getApplicationPool(applicationKey)
	if pool == nil {
		return nil
	}
	_, _, idleRunningPods := appc.groupApplicationPods(applicationKey)
	pendingSessions, deletingSessions, _, timeoutSessions, runningSessions := pool.groupSessionsByState()
	klog.InfoS("Syncing application session", "application", applicationKey, "#running", len(runningSessions), "#pending", len(pendingSessions), "#deleting", len(deletingSessions), "#timeout", len(timeoutSessions), "#idleRunningPods", len(idleRunningPods))

	sessionErrors := []error{}
	// 1/ assign pending sessions to idle pod
	si := 0
	for _, rp := range idleRunningPods {
		pod := appc.podManager.FindPod(rp.podName)
		if pod != nil {
			// allow only one sssion for one pod for now
			if rp.sessions.Len() == 0 {
				// have assigned all pending sessions, return
				if si == len(pendingSessions) {
					break
				}

				// update session status and set access point of session
				session := pendingSessions[si]
				err := appc.bindSessionToPod(applicationKey, pod, session)
				if err != nil {
					// move to next pod, it could fail to accept other session also
					klog.ErrorS(err, "Failed to open session on pod", "app", applicationKey, "session", session.Name, "pod", util.Name(pod))
					sessionErrors = append(sessionErrors, err)
					continue
				} else {
					rp.sessions.Add(string(session.GetUID()))
					si += 1
				}
			}
		}
	}

	// 2, cleanup timeout session, set session status to timeout and delete it from list
	for _, v := range timeoutSessions {
		if err := appc.changeSessionStatus(v, fornaxv1.SessionStatusTimeout); err != nil {
			klog.ErrorS(err, "Failed to cleanup timeout session")
			sessionErrors = append(sessionErrors, err)
		}
	}

	// 3, cleanup deleting session,
	for _, v := range deletingSessions {
		err := appc.deleteApplicationSession(applicationKey, v)
		if err != nil {
			klog.ErrorS(err, "Failed to delete deleting session")
			sessionErrors = append(sessionErrors, err)
		}
	}

	if len(sessionErrors) > 0 {
		return fmt.Errorf("Some sessions failed to be sync, errors=%v", sessionErrors)
	}

	return nil
}

// if session is open, close it and wait for node report back
// if session is still in pending, change status to timeout
// if session is not open or pending, just delete since it's already in a terminal state
func (appc *ApplicationManager) deleteApplicationSession(applicationKey string, session *fornaxv1.ApplicationSession) error {
	// sessionId := util.Name(session)
	sessionId := string(session.GetUID())
	// in terminal or pending state, just delete
	pool := appc.getApplicationPool(applicationKey)
	if pool == nil {
		return nil
	}

	if util.SessionIsOpen(session) {
		// TODO, if pod never report, session should be closed
		return appc.closeApplicationSession(session)
	} else if util.SessionIsPending(session) {
		err := appc.changeSessionStatus(session, fornaxv1.SessionStatusTimeout)
		if err != nil {
			return err
		}
	} else {
		// no op
	}

	if session.Status.PodReference != nil {
		pod := pool.getPod(session.Status.PodReference.Name)
		if pod != nil {
			pod.sessions.Delete(sessionId)
		}
	}
	pool.deleteSession(sessionId)
	return nil
}

// change sessions status to starting and set access point
func (appc *ApplicationManager) bindSessionToPod(applicationKey string, pod *v1.Pod, session *fornaxv1.ApplicationSession) error {
	klog.InfoS("Assign session to pod", "application", applicationKey, "pod", util.Name(pod), "session", util.Name(session))
	newStatus := session.Status.DeepCopy()
	newStatus.SessionStatus = fornaxv1.SessionStatusStarting
	for _, cont := range pod.Spec.Containers {
		for _, port := range cont.Ports {
			newStatus.AccessEndPoints = append(session.Status.AccessEndPoints, fornaxv1.AccessEndPoint{
				Protocol:  port.Protocol,
				IPAddress: port.HostIP,
				Port:      port.HostPort,
			})
		}
	}
	newStatus.PodReference = &v1.LocalObjectReference{
		Name: util.Name(pod),
	}
	oldStatus := session.Status.DeepCopy()
	session.Status = *newStatus
	if err := appc.sessionManager.OpenSession(pod, session); err != nil {
		// set status back in memory to try rebind
		session.Status = *oldStatus
		return err
	} else {
		return nil
	}
}

func (appc *ApplicationManager) closeApplicationSession(session *fornaxv1.ApplicationSession) error {
	klog.Infof("Close applciation sessions %s", util.Name(session))
	if util.SessionIsOpen(session) {
		if session.Status.PodReference != nil {
			podName := session.Status.PodReference.Name
			pod := appc.podManager.FindPod(podName)
			if pod != nil {
				// ideally this state should report back from node, set it here to avoid calling node to close session multiple times
				// if node report back different status, then app will call close session again
				session.Status.SessionStatus = fornaxv1.SessionStatusClosing
				return appc.sessionManager.CloseSession(pod, session)
			} else {
				// how to handle it, this case could happen when FornaxCore restart
				// it have not get all pods reported by node, and client want to close a session
				appc.changeSessionStatus(session, fornaxv1.SessionStatusClosed)
				return nil
			}
		}
	} else {
		// no op
	}

	return nil
}

func (appc *ApplicationManager) sessionHouseKeeping() error {
	apps := appc.applicationList()
	klog.Info("cleanup timeout session")
	for _, v := range apps {
		_, _, _, timeoutSessions, _ := v.groupSessionsByState()
		for _, v := range timeoutSessions {
			appc.changeSessionStatus(v, fornaxv1.SessionStatusTimeout)
		}
	}

	return nil
}

// cleanupSessionOnDeletedPod handle pod is terminated unexpectedly, e.g. node crash
// in normal cases,session should be closed before pod is terminated and deleted.
// It update open session to closed and pending session to timedout,
// and does not try to call node to close session, as session does not exist at all on node when pod deleted
func (appc *ApplicationManager) cleanupSessionOnDeletedPod(pool *ApplicationPool, podName string) error {
	klog.Infof("Delete all sessions of deleted pod %s", podName)
	sessions := pool.sessionList()
	podSessions := []*fornaxv1.ApplicationSession{}
	for _, v := range sessions {
		if v.Status.PodReference != nil && v.Status.PodReference.Name == podName {
			klog.Infof("Delete sessions %s", util.Name(v))
			pool.deleteSession(string(v.GetUID()))
			podSessions = append(podSessions, v)
		}
	}
	// use go routine to update session status, as session has been remove from pool,
	// update dead session status later will not impact sync application result
	go func() {
		for _, v := range podSessions {
			if util.SessionIsOpen(v) {
				appc.changeSessionStatus(v, fornaxv1.SessionStatusClosed)
			} else if util.SessionIsPending(v) {
				appc.changeSessionStatus(v, fornaxv1.SessionStatusTimeout)
			}
		}
	}()

	return nil
}

// cleanupSessionOfApplication if a application is being deleted,
// close all sessions which are still alive and delete sessions from application sessions pool if they are still pending
// when alive session reported as closed by Node Agent, then session can be eventually deleted
func (appc *ApplicationManager) cleanupSessionOfApplication(applicationKey string) error {
	klog.Infof("Delete all sessions of application %s", applicationKey)
	deleteErrors := []error{}

	pool := appc.getApplicationPool(applicationKey)
	if pool == nil {
		return nil
	}
	sessions := pool.sessionList()
	for _, v := range sessions {
		err := appc.deleteApplicationSession(applicationKey, v)
		if err != nil {
			deleteErrors = append(deleteErrors, err)
		}
	}

	if len(deleteErrors) != 0 {
		return fmt.Errorf("Some sessions failed to be deleted, errors=%v", deleteErrors)
	}

	return nil
}
