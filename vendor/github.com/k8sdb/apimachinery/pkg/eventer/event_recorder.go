package eventer

import (
	"github.com/appscode/log"
	kapi "k8s.io/kubernetes/pkg/api"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/client/record"
	"k8s.io/kubernetes/pkg/runtime"
)

const (
	EventReasonCreating             string = "Creating"
	EventReasonDeleting             string = "Deleting"
	EventReasonWipingOut            string = "WipingOut"
	EventReasonFailedToCreate       string = "Failed"
	EventReasonFailedToDelete       string = "Failed"
	EventReasonFailedToWipeOut      string = "Failed"
	EventReasonFailedToGet          string = "Failed"
	EventReasonFailedToInitialize   string = "Failed"
	EventReasonFailedToList         string = "Failed"
	EventReasonFailedToRecover      string = "Failed"
	EventReasonFailedToSchedule     string = "Failed"
	EventReasonFailedToStart        string = "Failed"
	EventReasonFailedToUpdate       string = "Failed"
	EventReasonIgnoredSnapshot      string = "IgnoredSnapshot"
	EventReasonInitializing         string = "Initializing"
	EventReasonInvalid              string = "Invalid"
	EventReasonInvalidUpdate        string = "InvalidUpdate"
	EventReasonSnapshotFailed       string = "SnapshotFailed"
	EventReasonStarting             string = "Starting"
	EventReasonSuccessfulCreate     string = "SuccessfulCreate"
	EventReasonSuccessfulDelete     string = "SuccessfulDelete"
	EventReasonSuccessfulWipeOut    string = "SuccessfulWipeOut"
	EventReasonSuccessfulSnapshot   string = "SuccessfulSnapshot"
	EventReasonSuccessfulValidate   string = "SuccessfulValidate"
	EventReasonSuccessfulInitialize string = "SuccessfulInitialize"
)

type EventRecorderInterface interface {
	PushEvent(eventtype, reason, message string, objects ...runtime.Object)
}

type eventRecorder struct {
	// Event Recorder
	record.EventRecorder
}

func NewEventRecorder(client clientset.Interface, component string) EventRecorderInterface {
	// Event Broadcaster
	broadcaster := record.NewBroadcaster()
	broadcaster.StartEventWatcher(
		func(event *kapi.Event) {
			if _, err := client.Core().Events(event.Namespace).Create(event); err != nil {
				log.Errorln(err)
			}
		},
	)
	// Event Recorder
	return &eventRecorder{broadcaster.NewRecorder(kapi.EventSource{Component: component})}
}

func (e *eventRecorder) PushEvent(eventtype, reason, message string, objects ...runtime.Object) {
	for _, obj := range objects {
		e.Event(obj, eventtype, reason, message)
	}
}
