package util

import (
	"fmt"
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
func CopyDir(source string, destination string) *command.Model {
	copyCmd := command.New("cp", "-R", source, destination)

	return copyCmd
}
