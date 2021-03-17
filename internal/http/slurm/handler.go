package slurm

import (
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
)

type SbatchHandler struct {
	handler handler
}

type JobStateHandler struct {
	handler handler
}

type ScancelHandler struct {
	handler handler
}

func (s SbatchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.logger.Infof("sbatch")

	commands := []string{
		"sbatch",
		"--parsable",
		"-J",
		"k8s-slurm-injector",
	}
	sshOptions := []string{
		"-o",
		"StrictHostKeyChecking=no",
		"-i",
		"/root/.ssh/id_rsa",
		"-p",
		s.handler.sshPort,
		s.handler.sshDestination,
	}
	sshCommand := exec.Command("ssh", append(sshOptions, commands...)...)
	stdin, _ := sshCommand.StdinPipe()
	_, _ = io.WriteString(stdin, "#!/bin/sh\n sleep 100")
	_ = stdin.Close()
	sshOut, sshErr := sshCommand.Output()
	if sshErr != nil {
		s.handler.logger.Errorf(sshErr.Error())
	}

	_, err := fmt.Fprintf(w, string(sshOut))
	if err != nil {
		s.handler.logger.Errorf("failed to print")
	}
}

func (h handler) sbatch() (http.Handler, error) {
	if h.sshDestination == "" {
		return nil, fmt.Errorf("ssh destination must be set")
	}

	return SbatchHandler{h}, nil
}

func (s JobStateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	jobid := r.URL.Query().Get("jobid")
	s.handler.logger.Infof("state jobid=%s", jobid)

	if jobid == "" {
		s.handler.logger.Errorf("jobid is not given")
		return
	}

	// Execute `scontrol show jobid ???`
	commands := []string{
		"scontrol",
		"show",
		"jobid",
		jobid,
	}
	sshOptions := []string{
		"-o",
		"StrictHostKeyChecking=no",
		"-i",
		"/root/.ssh/id_rsa",
		"-p",
		s.handler.sshPort,
		s.handler.sshDestination,
	}
	sshCommand := exec.Command("ssh", append(sshOptions, commands...)...)
	sshOut, sshErr := sshCommand.Output()
	if sshErr != nil {
		s.handler.logger.Errorf(sshErr.Error())
	}

	// Extract JobState
	reg := regexp.MustCompile(`(?i)JobState=[A-Z]+`)
	matched := reg.Find(sshOut)
	if matched == nil {
		s.handler.logger.Errorf("failed to extract job-state")
		return
	}
	state := strings.Split(string(matched), "=")[1]

	_, err := fmt.Fprintf(w, state)
	if err != nil {
		s.handler.logger.Errorf("failed to print")
	}
}

func (h handler) jobState() (http.Handler, error) {
	if h.sshDestination == "" {
		return nil, fmt.Errorf("ssh destination must be set")
	}

	return JobStateHandler{h}, nil
}

func (s ScancelHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	jobid := r.URL.Query().Get("jobid")
	s.handler.logger.Infof("scancel jobid=%s", jobid)

	if jobid == "" {
		s.handler.logger.Errorf("jobid is not given")
		return
	}

	// Execute `scontrol show jobid ???`
	commands := []string{
		"scancel",
		jobid,
	}
	sshOptions := []string{
		"-o",
		"StrictHostKeyChecking=no",
		"-i",
		"/root/.ssh/id_rsa",
		"-p",
		s.handler.sshPort,
		s.handler.sshDestination,
	}
	sshCommand := exec.Command("ssh", append(sshOptions, commands...)...)
	sshOut, sshErr := sshCommand.Output()
	if sshErr != nil {
		s.handler.logger.Errorf(sshErr.Error())
	}

	_, err := fmt.Fprintf(w, string(sshOut))
	if err != nil {
		s.handler.logger.Errorf("failed to print")
	}
}

func (h handler) scancel() (http.Handler, error) {
	if h.sshDestination == "" {
		return nil, fmt.Errorf("ssh destination must be set")
	}

	return ScancelHandler{h}, nil
}
