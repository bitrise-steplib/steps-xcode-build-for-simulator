package util

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/bitrise-io/go-utils/command"
)

type coloringFunc func(...interface{}) string

func currentTimestamp() string {
	timeStampFormat := "15:04:05"
	currentTime := time.Now()
	return currentTime.Format(timeStampFormat)
}

// LogWithTimestamp ...
func LogWithTimestamp(coloringFunc coloringFunc, format string, v ...interface{}) {
	message := fmt.Sprintf(format, v...)
	messageWithTimeStamp := fmt.Sprintf("[%s] %s", currentTimestamp(), coloringFunc(message))
	fmt.Println(messageWithTimeStamp)
}

// CopyDir ...
func CopyDir(source string, destination string) error {
	copyCmd := command.New("cp", "-R", source, destination)
	copyCmd.SetStdout(os.Stdout)
	copyCmd.SetStderr(os.Stderr)

	log.Printf(copyCmd.PrintableCommandArgs())

	return copyCmd.Run()
}
