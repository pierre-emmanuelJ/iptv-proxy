package hls

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"syscall"

	log "github.com/sirupsen/logrus"
)

func execute(cmdPath string, args []string) (dataout []byte, dataerr []byte, err error) {
	cmd := exec.Command(cmdPath, args...)

	stdout, err1 := cmd.StdoutPipe()
	defer stdout.Close()
	if err1 != nil {
		err = fmt.Errorf("Error opening stdout of command: %v", err)
		return
	}

	stderr, err11 := cmd.StderrPipe()
	defer stdout.Close()
	if err11 != nil {
		err = fmt.Errorf("Error opening stdout of command: %v", err)
		return
	}

	log.Debugf("Executing: %v %v", cmdPath, args)
	err2 := cmd.Start()
	if err2 != nil {
		err = fmt.Errorf("Error starting command: %v", err)
		return
	}

	var buffer bytes.Buffer
	_, err3 := io.Copy(&buffer, stdout)
	if err3 != nil {
		// Ask the process to exit
		cmd.Process.Signal(syscall.SIGKILL)
		cmd.Process.Wait()
		err = fmt.Errorf("Error copying stdout to buffer: %v", err)
		return
	}

	var buffererr bytes.Buffer
	_, err33 := io.Copy(&buffererr, stderr)
	if err33 != nil {
		// Ask the process to exit
		cmd.Process.Signal(syscall.SIGKILL)
		cmd.Process.Wait()
		err = fmt.Errorf("Error copying stdout to buffer: %v", err)
		return
	}

	err4 := cmd.Wait()
	if err4 != nil {
		dataerr = buffererr.Bytes()
		err = fmt.Errorf("Command failed %v", err4)
		return
	}

	dataout = buffer.Bytes()
	return
}
