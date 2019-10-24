package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bitrise-io/go-utils/colorstring"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-xcode/xcodebuild"
	cache "github.com/bitrise-io/go-xcode/xcodecache"
	"github.com/bitrise-io/go-xcode/xcpretty"
	"github.com/bitrise-steplib/steps-xcode-build-for-simulator/util"
)

func runBuildCommandWithRetry(buildCmd *xcodebuild.CommandBuilder, useXcpretty bool, swiftPackagesPath string) (string, error) {
	output, err := runBuildCommand(buildCmd, useXcpretty)
	if err != nil && swiftPackagesPath != "" && strings.Contains(output, cache.SwiftPackagesStateInvalid) {
		log.Warnf("Archive failed, swift packages cache is in an invalid state, error: %s", err)
		log.RWarnf("xcode-archive", "swift-packages-cache-invalid", nil, "swift packages cache is in an invalid state")
		if err := os.RemoveAll(swiftPackagesPath); err != nil {
			return output, fmt.Errorf("failed to remove invalid Swift package caches, error: %s", err)
		}
		return runBuildCommand(buildCmd, useXcpretty)
	}
	return output, err
}

func runBuildCommand(buildCmd *xcodebuild.CommandBuilder, useXcpretty bool) (string, error) {
	if useXcpretty {
		xcprettyCmd := xcpretty.New(buildCmd)
		util.LogWithTimestamp(colorstring.Green, "$ %s", xcprettyCmd.PrintableCmd())
		fmt.Println()

		return xcprettyCmd.Run()
	}
	util.LogWithTimestamp(colorstring.Green, "$ %s", buildCmd.PrintableCmd())
	fmt.Println()

	buildRootCmd := buildCmd.Command()
	var output bytes.Buffer
	buildRootCmd.SetStdout(io.MultiWriter(os.Stdout, &output))
	buildRootCmd.SetStderr(io.MultiWriter(os.Stderr, &output))

	return output.String(), buildRootCmd.Run()
}
