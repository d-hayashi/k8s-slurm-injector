package slurm_handler

import (
	"testing"

	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/sidecar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSbatchHandler_constructCommand(t *testing.T) {
	tests := map[string]struct {
		obj    sidecar.JobInformation
		expObj []string
	}{
		"case1": {
			sidecar.JobInformation{
				Partition: "partition1",
			},
			[]string{
				"sbatch",
				"--parsable",
				"--output=/dev/null",
				"--error=/dev/null",
				"--job-name=k8s-slurm-injector-job",
				"--partition=partition1",
			},
		},
		"case2": {
			sidecar.JobInformation{
				Partition: "partition1",
				Node:      "node1",
				Ntasks:    "2",
				Ncpus:     "3",
				Gres:      "gpu:1",
				Time:      "3600",
				Name:      "test",
			},
			[]string{
				"sbatch",
				"--parsable",
				"--output=/dev/null",
				"--error=/dev/null",
				"--job-name=test",
				"--partition=partition1",
				"--nodelist=node1",
				"--ntasks=2",
				"--cpus-per-task=3",
				"--gres=gpu:1",
				"--time=3600",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)

			// Parse query parameters
			jobInfo := test.obj
			var commands []string
			commands, err := constructCommand(&jobInfo)
			require.Empty(err)

			// Check parsing results
			assert.Equal(commands, test.expObj)
		})
	}
}
