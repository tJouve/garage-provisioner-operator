package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReadyConditionNeedsUpdate(t *testing.T) {
	ready := metav1.Condition{
		Type:               readyConditionType,
		Status:             metav1.ConditionTrue,
		Reason:             "BucketReady",
		Message:            "Garage bucket is in desired state",
		ObservedGeneration: 3,
	}

	tests := []struct {
		name               string
		conditions         []metav1.Condition
		observedGeneration int64
		generation         int64
		reason             string
		message            string
		want               bool
	}{
		{"stable", []metav1.Condition{ready}, 3, 3, ready.Reason, ready.Message, false},
		{"first ready", nil, 0, 3, ready.Reason, ready.Message, true},
		{"not ready to ready", []metav1.Condition{{Type: readyConditionType, Status: metav1.ConditionFalse, ObservedGeneration: 3}}, 3, 3, ready.Reason, ready.Message, true},
		{"reason changed", []metav1.Condition{ready}, 3, 3, "OtherReason", ready.Message, true},
		{"message changed", []metav1.Condition{ready}, 3, 3, ready.Reason, "changed", true},
		{"generation changed", []metav1.Condition{ready}, 3, 4, ready.Reason, ready.Message, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readyConditionNeedsUpdate(tt.conditions, tt.observedGeneration, tt.generation, tt.reason, tt.message); got != tt.want {
				t.Fatalf("readyConditionNeedsUpdate() = %v, want %v", got, tt.want)
			}
		})
	}
}
