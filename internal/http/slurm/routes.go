package slurm

import (
	"net/http"
)

// routes wires the routes to handlers on a specific router.
func (h handler) routes(router *http.ServeMux) error {
	isInjectable, err := h.isInjectable()
	if err != nil {
		return err
	}
	router.Handle("/slurm/isinjectable", isInjectable)

	sbatch, err := h.sbatch()
	if err != nil {
		return err
	}
	router.Handle("/slurm/sbatch", sbatch)

	jobEnv, err := h.jobEnv()
	if err != nil {
		return err
	}
	router.Handle("/slurm/env", jobEnv)

	jobEnvToConfigMap, err := h.jobEnvToConfigMap()
	if err != nil {
		return err
	}
	router.Handle("/slurm/envtoconfigmap", jobEnvToConfigMap)

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
