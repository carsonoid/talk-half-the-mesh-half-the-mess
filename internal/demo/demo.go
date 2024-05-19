package demo

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// RunShellScript runs a shell script in a given directory
func RunShellScript(basePath string, script string) error {
	ctx, cancel := context.WithCancel(context.Background())

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-signalChan
		cancel()
		<-signalChan
		os.Exit(1)
	}()

	cwd := os.Getenv("PWD")
	defer os.Chdir(cwd)

	err := os.Chdir(basePath)
	if err != nil {
		return err
	}

	tmpFile, err := os.Create("tmp.sh")
	if err != nil {
		return err
	}
	defer tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(script)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "bash", "-e", "-x", tmpFile.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	if err != nil {
		return err
	}

	defer cmd.Process.Signal(syscall.SIGTERM)
	// send group signal to all processes in the process group
	defer syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)

	err = cmd.Wait()
	if err != nil {
		return err
	}
	return nil
}
