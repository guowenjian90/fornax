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

package v1

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/apiserver-runtime/pkg/builder/resource"
	"sigs.k8s.io/apiserver-runtime/pkg/builder/resource/resourcestrategy"
)

// ApplicationSession
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:openapi-gen=true
type ApplicationSession struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   ApplicationSessionSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status ApplicationSessionStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// ApplicationSessionList
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:openapi-gen=true
type ApplicationSessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []ApplicationSession `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// ApplicationSessionSpec defines the desired state of ApplicationSession
type ApplicationSessionSpec struct {

	// ApplicationName, client provided application
	ApplicationName string `json:"applicationName,omitempty" protobuf:"bytes,1,opt,name=applicationName"`

	// Session data is a base64 string pass through into application instances when session started
	// +optional
	SessionData string `json:"sessionData,omitempty" protobuf:"bytes,2,opt,name=sessionData"`

	// if a application instance evacuated all session, kill it, default true
	KillInstanceWhenSessionClosed bool `json:"killInstanceWhenSessionClosed,omitempty" protobuf:"varint,3,opt,name=killInstanceWhenSessionClosed"`

	// how long to wait for before close session, default 60
	CloseGracePeriodSeconds *uint32 `json:"closeGracePeriodSeconds,omitempty" protobuf:"varint,4,opt,name=closeGracePeriodSeconds"`

	// how long to wait for session status from Starting to Available
	OpenTimeoutSeconds uint32 `json:"openTimeoutSeconds,omitempty" protobuf:"varint,5,opt,name=openTimeoutSeconds"`
}

// +enum
type SessionStatus string

const (
	// session is not allocated yet
	SessionStatusUnspecified SessionStatus = ""

	// session is not allocated yet
	SessionStatusPending SessionStatus = "Pending"

	// session is send to instance, waiting for instance report session state
	SessionStatusStarting SessionStatus = "Starting"

	// session is started on instance, not used yet
	SessionStatusAvailable SessionStatus = "Available"

	// session is started on instance, session is being used
	SessionStatusInUse SessionStatus = "InUse"

	// session is closing on instance, wait for session client exit
	SessionStatusClosing SessionStatus = "Closing"

	// session is closed on instance
	SessionStatusClosed SessionStatus = "Closed"

	// session is dead, no heartbeat, should close and start a new one
	SessionStatusTimeout SessionStatus = "Timeout"
)

type AccessEndPoint struct {
	// TCP/UDP
	Protocol v1.Protocol `json:"protocol,omitempty" protobuf:"bytes,1,opt,name=protocol,casttype=k8s.io/api/core/v1.Protocol"`

	// IPaddress
	IPAddress string `json:"ipAddress,omitempty" protobuf:"bytes,2,opt,name=ipAddress"`

	// Port
	Port int32 `json:"port,omitempty" protobuf:"varint,3,opt,name=port"`
}

// ApplicationSessionStatus defines the observed state of ApplicationSession
type ApplicationSessionStatus struct {
	// Endpoint this session is using
	// +optional
	// +listType=atomic
	AccessEndPoints []AccessEndPoint `json:"accessEndPoints,omitempty" protobuf:"bytes,1,rep,name=accessEndPoints"`

	// Session status, is Starting, Available or Closed.
	// +optional
	SessionStatus SessionStatus `json:"sessionStatus,omitempty" protobuf:"bytes,2,opt,name=sessionStatus,casttype=SessionStatus"`

	// +optional
	// +listType=set
	ClientSessions []corev1.LocalObjectReference `json:"clientSessions,omitempty" protobuf:"bytes,3,rep,name=clientSessions"`

	// +optional
	AvailableTime *metav1.Time `json:"availableTime,omitempty" protobuf:"bytes,4,opt,name=availableTime"`

	// +optional
	CloseTime *metav1.Time `json:"closeTime,omitempty" protobuf:"bytes,5,opt,name=closeTime"`

	// +optional, for metrics test
	AvailableTimeMicro int64 `json:"availableTimeMicro,omitempty" protobuf:"varint,6,opt,name=availableTimeMicro"`
}

var _ resource.Object = &ApplicationSession{}
var _ resourcestrategy.Validater = &ApplicationSession{}

func (in *ApplicationSession) GetObjectMeta() *metav1.ObjectMeta {
	return &in.ObjectMeta
}

func (in *ApplicationSession) NamespaceScoped() bool {
	return true
}

func (in *ApplicationSession) New() runtime.Object {
	return &ApplicationSession{}
}

func (in *ApplicationSession) NewList() runtime.Object {
	return &ApplicationSessionList{}
}

var ApplicationSessionGrv = schema.GroupVersionResource{
	Group:    "core.fornax-serverless.centaurusinfra.io",
	Version:  "v1",
	Resource: "applicationsessions",
}

var ApplicationSessionKind = SchemeGroupVersion.WithKind("ApplicationSession")
var ApplicationSessionGrvKey = fmt.Sprintf("/%s/%s", ApplicationSessionGrv.Group, ApplicationSessionGrv.Resource)

func (in *ApplicationSession) GetGroupVersionResource() schema.GroupVersionResource {
	return ApplicationSessionGrv
}

func (in *ApplicationSession) IsStorageVersion() bool {
	return true
}

func (in *ApplicationSession) Validate(ctx context.Context) field.ErrorList {
	errorList := make(field.ErrorList, 0)
	if len(in.Spec.ApplicationName) == 0 {
		err := field.Error{
			Type:  field.ErrorTypeRequired,
			Field: "Spec.ApplicationName",
		}
		errorList = append(errorList, &err)
	}

	if len(in.Spec.SessionData) == 0 {
		err := field.Error{
			Type:  field.ErrorTypeRequired,
			Field: "Spec.SessionData",
		}
		errorList = append(errorList, &err)
	}

	if len(errorList) > 0 {
		return errorList
	} else {
		return nil
	}
}

var _ resource.ObjectList = &ApplicationSessionList{}

func (in *ApplicationSessionList) GetListMeta() *metav1.ListMeta {
	return &in.ListMeta
}

func (in ApplicationSessionStatus) SubResourceName() string {
	return "status"
}

// ApplicationSession implements ObjectWithStatusSubResource interface.
var _ resource.ObjectWithStatusSubResource = &ApplicationSession{}

func (in *ApplicationSession) GetStatus() resource.StatusSubResource {
	return in.Status
}

// ApplicationSessionStatus{} implements StatusSubResource interface.
var _ resource.StatusSubResource = &ApplicationSessionStatus{}

func (in ApplicationSessionStatus) CopyTo(parent resource.ObjectWithStatusSubResource) {
	parent.(*ApplicationSession).Status = in
}
