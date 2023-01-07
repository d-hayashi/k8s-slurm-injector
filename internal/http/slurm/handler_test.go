package slurm

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/sidecar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSbatchHandler_parseQueryParams(t *testing.T) {
	tests := map[string]struct {
		obj    sidecar.JobInformation
		expObj sidecar.JobInformation
	}{
		"case1": {
			sidecar.JobInformation{
				Partition: "partition1",
			},
			sidecar.JobInformation{
				Partition: "partition1",
			},
		},
		"case2": {
			sidecar.JobInformation{
				Partition: "partition1 ",
			},
			sidecar.JobInformation{
				Partition: "partition1",
			},
		},
		"case3": {
			sidecar.JobInformation{
				Ncpus: "1",
			},
			sidecar.JobInformation{
				Ncpus: "1",
			},
		},
		"case4": {
			sidecar.JobInformation{
				Ncpus: "1x",
			},
			sidecar.JobInformation{
				Ncpus: "1",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)

			// Arrange
			reqBody := bytes.NewBufferString("request body")
			req := httptest.NewRequest(http.MethodGet, "/slurm/sbatch", reqBody)

			// Construct URL
			q := req.URL.Query()
			q.Add("partition", test.obj.Partition)
			q.Add("node", test.obj.Node)
			q.Add("ntasks", test.obj.Ntasks)
			q.Add("ncpus", test.obj.Ncpus)
			q.Add("ngpus", test.obj.Gres)
			q.Add("time", test.obj.Time)
			q.Add("name", test.obj.Name)
			req.URL.RawQuery = q.Encode()

			// Parse query parameters
			jobInfo := sidecar.NewJobInformation()
			err := parseQueryParams(req, jobInfo)
			require.Empty(err)

			// Check parsing results
			assert.Equal(jobInfo.Partition, test.expObj.Partition)
			assert.Equal(jobInfo.Node, test.expObj.Node)
			assert.Equal(jobInfo.Ntasks, test.expObj.Ntasks)
			assert.Equal(jobInfo.Ncpus, test.expObj.Ncpus)
			assert.Equal(jobInfo.Gres, test.expObj.Gres)
			assert.Equal(jobInfo.Time, test.expObj.Time)
			assert.Equal(jobInfo.Name, test.expObj.Name)
		})
	}
}
