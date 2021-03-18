package slurm

import (
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"

	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/sidecar"
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

func constructSSHCommand(sshPort string, sshDestination string, commands []string) *exec.Cmd {
	sshOptions := []string{
		"-o",
		"StrictHostKeyChecking=no",
		//"-i",
		//"/root/.ssh/id_rsa",
		"-p",
		sshPort,
		sshDestination,
	}

	return exec.Command("ssh", append(sshOptions, commands...)...)
}

func (s SbatchHandler) parseQueryParams(r *http.Request, jobInfo *sidecar.JobInformation) error {
	regString := regexp.MustCompile(`[^0-9A-Za-z_#:-]`)
	regDecimal := regexp.MustCompile(`[^0-9]`)
	jobInfo.Partition = regString.ReplaceAllString(r.URL.Query().Get("partition"), "")
	jobInfo.Node = regString.ReplaceAllString(r.URL.Query().Get("node"), "")
	jobInfo.Ntasks = regDecimal.ReplaceAllString(r.URL.Query().Get("ntasks"), "")
	jobInfo.Ncpus = regDecimal.ReplaceAllString(r.URL.Query().Get("ncpus"), "")
	jobInfo.Gres = regString.ReplaceAllString(r.URL.Query().Get("gres"), "")
	jobInfo.Time = regDecimal.ReplaceAllString(r.URL.Query().Get("time"), "")
	jobInfo.Name = regString.ReplaceAllString(r.URL.Query().Get("name"), "")

	return nil
}

func (s SbatchHandler) constructCommand(jobInfo *sidecar.JobInformation, commands *[]string) error {
	jobName := jobInfo.Name
	if jobName == "" {
		jobName = "k8s-slurm-injector-job"
	}

	*commands = []string{
		"sbatch",
		"--parsable",
		"--output=/dev/null",
		"--error=/dev/null",
		fmt.Sprintf("--job-name=%s", jobName),
	}
	if jobInfo.Partition != "" {
		*commands = append(*commands, fmt.Sprintf("--partition=%s", jobInfo.Partition))
	}
	if jobInfo.Node != "" {
		*commands = append(*commands, fmt.Sprintf("--nodelist=%s", jobInfo.Node))
	}
	if jobInfo.Ntasks != "" {
		*commands = append(*commands, fmt.Sprintf("--ntasks=%s", jobInfo.Ntasks))
	}
	if jobInfo.Ncpus != "" {
		*commands = append(*commands, fmt.Sprintf("--cpus-per-task=%s", jobInfo.Ncpus))
	}
	if jobInfo.Gres != "" {
		*commands = append(*commands, fmt.Sprintf("--gres=%s", jobInfo.Gres))
	}
	if jobInfo.Time != "" {
		*commands = append(*commands, fmt.Sprintf("--time=%s", jobInfo.Time))
	}

	return nil
}

func (s SbatchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.logger.Infof("sbatch")

	var out []byte
	var err error = nil
	var commands []string
	jobInfo := sidecar.NewJobInformation()

	// Parse request and construct commands
	err = s.parseQueryParams(r, jobInfo)
	if err == nil {
		err = s.constructCommand(jobInfo, &commands)
	}

	// Execute ssh commands
	sshCommand := constructSSHCommand(s.handler.sshPort, s.handler.sshDestination, commands)
	stdin, _ := sshCommand.StdinPipe()
	_, _ = io.WriteString(
		stdin,
		"#!/bin/sh "+
			"\n trap 'echo \"Killing...\"' SIGHUP SIGINT SIGQUIT SIGTERM "+
			"\n echo \"Sleeping.  Pid=$$\" "+
			"\n while true; do sleep 10 & wait $!; done",
	)
	_ = stdin.Close()
	if err == nil {
		out, err = sshCommand.Output()
	}

	// Write to respond
	_, err = fmt.Fprintf(w, string(out))

	// Handle error
	if err != nil {
		s.handler.logger.Errorf("failed to sbatch: %s", err.Error())
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

	var out []byte
	var state string
	var err error = nil

	// Execute `scontrol show jobid ???`
	commands := []string{
		"scontrol",
		"show",
		"jobid",
		jobid,
	}
	sshCommand := constructSSHCommand(s.handler.sshPort, s.handler.sshDestination, commands)
	out, err = sshCommand.Output()

	// Extract JobState
	reg := regexp.MustCompile(`(?i)JobState=[A-Z]+`)
	matched := reg.Find(out)
	if matched != nil {
		state = strings.Split(string(matched), "=")[1]
	}

	// Write to respond
	_, err = fmt.Fprintf(w, state)

	// Handle error
	if err != nil {
		s.handler.logger.Errorf("failed to get job information: %s", err.Error())
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

	var out []byte
	var err error = nil

	// Execute `scancel {jobid}`
	sshCommand := constructSSHCommand(s.handler.sshPort, s.handler.sshDestination, []string{"scancel", jobid})
	out, err = sshCommand.Output()

	// Write to respond
	_, err = fmt.Fprintf(w, string(out))

	// Handle error
	if err != nil {
		s.handler.logger.Errorf("failed to scancel: %s", err.Error())
	}
}

func (h handler) scancel() (http.Handler, error) {
	if h.sshDestination == "" {
		return nil, fmt.Errorf("ssh destination must be set")
	}

	return ScancelHandler{h}, nil
}
