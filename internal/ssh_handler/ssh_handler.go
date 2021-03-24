package ssh_handler

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

type SSHHandler interface {
	RunCommandCombined(command SSHCommand) ([]byte, error)
	RunCommand(command SSHCommand) ([]byte, error)
	Destruct() error
}

type SSHCommand struct {
	Command   string
	StdinPipe string
}

type SSH struct {
	destination string
	port        string
	conn        *ssh.Client
}

type DummySSH struct{}

func Dummy() (SSHHandler, error) {
	return DummySSH{}, nil
}

// Establish SSH connection
func New(dest string, port string) (SSHHandler, error) {
	// Init
	sshInfo := SSH{
		destination: dest,
		port:        port,
	}

	// Parse ssh-destination
	if !strings.Contains(dest, "@") {
		return nil, fmt.Errorf("SSH destination must be specified with format 'user@host'")
	}
	user := strings.Split(dest, "@")[0]
	host := strings.Split(dest, "@")[1]

	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	// Get id_rsa
	sshPrivKeyFile := filepath.Join(homeDir, ".ssh/id_rsa")
	privKey, err := ioutil.ReadFile(sshPrivKeyFile)
	if err != nil {
		return nil, err
	}

	// Parse id_rsa
	signer, err := ssh.ParsePrivateKey(privKey)
	if err != nil {
		return nil, err
	}

	// Establish SSH connection
	conn, err := ssh.Dial("tcp", host+":"+port, &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		return nil, err
	}
	sshInfo.conn = conn

	return &sshInfo, err
}

func (s SSH) RunCommandCombined(command SSHCommand) ([]byte, error) {
	var (
		session *ssh.Session
		err     error
		res     []byte
	)

	if session, err = s.conn.NewSession(); err != nil {
		return nil, err
	}
	defer session.Close()

	if command.StdinPipe != "" {
		go func() {
			stdin, _ := session.StdinPipe()
			defer stdin.Close()

			_, err = io.WriteString(stdin, command.StdinPipe)
		}()
	}

	res, err = session.CombinedOutput(command.Command)
	return res, err
}

func (s SSH) RunCommand(command SSHCommand) ([]byte, error) {
	var (
		session *ssh.Session
		err     error
		res     []byte
	)

	if session, err = s.conn.NewSession(); err != nil {
		return nil, err
	}
	defer session.Close()

	if command.StdinPipe != "" {
		go func() {
			stdin, _ := session.StdinPipe()
			defer stdin.Close()

			_, err = io.WriteString(stdin, command.StdinPipe)
		}()
	}

	res, err = session.Output(command.Command)
	return res, err
}

func (s SSH) Destruct() error {
	return s.conn.Close()
}

func (s DummySSH) RunCommandCombined(command SSHCommand) ([]byte, error) {
	return nil, nil
}

func (s DummySSH) RunCommand(command SSHCommand) ([]byte, error) {
	return nil, nil
}

func (s DummySSH) Destruct() error {
	return nil
}
