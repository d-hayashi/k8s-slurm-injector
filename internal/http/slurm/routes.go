package slurm

import (
	"net/http"
)

// routes wires the routes to handlers on a specific router.
func (h handler) routes(router *http.ServeMux) error {
	sbatch, err := h.sbatch()
	if err != nil {
		return err
	}
	router.Handle("/slurm/sbatch", sbatch)

	jobState, err := h.jobState()
	if err != nil {
		return err
	}
	router.Handle("/slurm/state", jobState)

	scancel, err := h.scancel()
	if err != nil {
		return err
	}
	router.Handle("/slurm/scancel", scancel)

	return nil
}
