package main

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/bitrise-io/go-utils/colorstring"
	"github.com/bitrise-io/go-utils/command"
	"github.com/bitrise-io/go-xcode/xcodebuild"
	"github.com/bitrise-io/go-xcode/xcpretty"
	"github.com/bitrise-steplib/steps-xcode-build-for-simulator/util"
)

func prepareCommand(xcodeCmd *xcodebuild.CommandBuilder, useXcpretty bool, output *bytes.Buffer) (*command.Model, *xcpretty.CommandModel) {
	if useXcpretty {
		return nil, xcpretty.New(*xcodeCmd)
	}

	buildRootCmd := xcodeCmd.Command()
	buildRootCmd.SetStdout(io.MultiWriter(os.Stdout, output))
	buildRootCmd.SetStderr(io.MultiWriter(os.Stderr, output))
	return buildRootCmd, nil
}

func runCommand(buildCmd *xcodebuild.CommandBuilder, useXcpretty bool) (string, error) {
	var output bytes.Buffer
	xcodebuildCmd, xcprettyCmd := prepareCommand(buildCmd, useXcpretty, &output)

	if xcprettyCmd != nil {
		util.LogWithTimestamp(colorstring.Green, "$ %s", xcprettyCmd.PrintableCmd())
		fmt.Println()
		return xcprettyCmd.Run()
	}
	util.LogWithTimestamp(colorstring.Green, "$ %s", xcodebuildCmd.PrintableCommandArgs())
	fmt.Println()
	return output.String(), xcodebuildCmd.Run()
}
