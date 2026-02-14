package imagebuild

import (
	"testing"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	knativev1 "knative.dev/pkg/apis/duck/v1"
)

func TestPipelineRunFailureMessage(t *testing.T) {
	tests := []struct {
		name        string
		pipelineRun *tektonv1.PipelineRun
		want        string
	}{
		{
			name: "returns condition message on failure",
			pipelineRun: &tektonv1.PipelineRun{
				Status: tektonv1.PipelineRunStatus{
					Status: knativev1.Status{
						Conditions: knativev1.Conditions{
							{
								Type:    conditionSucceeded,
								Status:  corev1.ConditionFalse,
								Message: "TaskRun build-step failed: container exited with code 1",
							},
						},
					},
				},
			},
			want: "Build failed: TaskRun build-step failed: container exited with code 1",
		},
		{
			name: "returns fallback when no conditions",
			pipelineRun: &tektonv1.PipelineRun{
				Status: tektonv1.PipelineRunStatus{},
			},
			want: "Build failed",
		},
		{
			name: "returns fallback when Succeeded condition has empty message",
			pipelineRun: &tektonv1.PipelineRun{
				Status: tektonv1.PipelineRunStatus{
					Status: knativev1.Status{
						Conditions: knativev1.Conditions{
							{
								Type:   conditionSucceeded,
								Status: corev1.ConditionFalse,
							},
						},
					},
				},
			},
			want: "Build failed",
		},
		{
			name: "ignores non-Succeeded conditions",
			pipelineRun: &tektonv1.PipelineRun{
				Status: tektonv1.PipelineRunStatus{
					Status: knativev1.Status{
						Conditions: knativev1.Conditions{
							{
								Type:    "Ready",
								Status:  corev1.ConditionFalse,
								Message: "not ready",
							},
						},
					},
				},
			},
			want: "Build failed",
		},
		{
			name: "ignores Succeeded=True condition",
			pipelineRun: &tektonv1.PipelineRun{
				Status: tektonv1.PipelineRunStatus{
					Status: knativev1.Status{
						Conditions: knativev1.Conditions{
							{
								Type:    conditionSucceeded,
								Status:  corev1.ConditionTrue,
								Message: "All Tasks have completed executing",
							},
						},
					},
				},
			},
			want: "Build failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pipelineRunFailureMessage(tt.pipelineRun)
			if got != tt.want {
				t.Errorf("pipelineRunFailureMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTaskRunFailureMessage(t *testing.T) {
	tests := []struct {
		name     string
		taskRun  *tektonv1.TaskRun
		fallback string
		want     string
	}{
		{
			name: "returns condition message on failure",
			taskRun: &tektonv1.TaskRun{
				Status: tektonv1.TaskRunStatus{
					Status: knativev1.Status{
						Conditions: knativev1.Conditions{
							{
								Type:    conditionSucceeded,
								Status:  corev1.ConditionFalse,
								Message: "step flash failed: timeout waiting for device",
							},
						},
					},
				},
			},
			fallback: "Flash to device failed",
			want:     "Flash to device failed: step flash failed: timeout waiting for device",
		},
		{
			name: "returns fallback when no conditions",
			taskRun: &tektonv1.TaskRun{
				Status: tektonv1.TaskRunStatus{},
			},
			fallback: "Flash to device failed",
			want:     "Flash to device failed",
		},
		{
			name: "returns fallback when Succeeded condition has empty message",
			taskRun: &tektonv1.TaskRun{
				Status: tektonv1.TaskRunStatus{
					Status: knativev1.Status{
						Conditions: knativev1.Conditions{
							{
								Type:   conditionSucceeded,
								Status: corev1.ConditionFalse,
							},
						},
					},
				},
			},
			fallback: "Flash to device failed",
			want:     "Flash to device failed",
		},
		{
			name: "ignores Succeeded=True condition",
			taskRun: &tektonv1.TaskRun{
				Status: tektonv1.TaskRunStatus{
					Status: knativev1.Status{
						Conditions: knativev1.Conditions{
							{
								Type:    conditionSucceeded,
								Status:  corev1.ConditionTrue,
								Message: "All steps completed",
							},
						},
					},
				},
			},
			fallback: "Flash to device failed",
			want:     "Flash to device failed",
		},
		{
			name: "uses custom fallback message",
			taskRun: &tektonv1.TaskRun{
				Status: tektonv1.TaskRunStatus{},
			},
			fallback: "Custom operation failed",
			want:     "Custom operation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := taskRunFailureMessage(tt.taskRun, tt.fallback)
			if got != tt.want {
				t.Errorf("taskRunFailureMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}
