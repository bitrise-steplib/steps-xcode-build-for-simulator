package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/bitrise-io/bitrise-build-cache-cli/v2/pkg/reactnative/wrap"
	"github.com/bitrise-io/go-utils/colorstring"
	"github.com/bitrise-io/go-utils/command"
	v2log "github.com/bitrise-io/go-utils/v2/log"
	"github.com/bitrise-io/go-xcode/xcodebuild"
	"github.com/bitrise-io/go-xcode/xcpretty"
	"github.com/bitrise-steplib/steps-xcode-build-for-simulator/util"
)

func prepareCommand(xcodeCmd *xcodebuild.CommandBuilder, useXcpretty bool, output *bytes.Buffer) (*command.Model, *xcpretty.CommandModel) {
	if useXcpretty {
		return nil, xcpretty.New(xcodeCmd)
	}

	buildRootCmd := xcodeCmd.Command()
	buildRootCmd.SetStdout(io.MultiWriter(os.Stdout, output))
	buildRootCmd.SetStderr(io.MultiWriter(os.Stderr, output))
	return buildRootCmd, nil
}

func runCommand(buildCmd *xcodebuild.CommandBuilder, useXcpretty bool) (string, error) {
	// When React Native build cache is active on this machine, route the
	// xcodebuild invocation through `bitrise-build-cache react-native run -- ...`
	// so it runs as a child of the active RN parent invocation. xcpretty piping
	// is preserved when the user picked xcpretty as the output tool — we just
	// pipe the wrapped command's stdout into xcpretty manually instead of
	// letting go-xcode's xcpretty.New build the pipeline. When RN cache is not
	// active, fall through to the existing v1 path unchanged.
	det := wrap.Detect(context.Background(), wrap.DetectParams{Logger: v2log.NewLogger()})
	if det.ReactNativeEnabled {
		return runWithRNWrap(buildCmd, det, useXcpretty)
	}

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

// runWithRNWrap runs xcodebuild under `bitrise-build-cache react-native run --`,
// preserving xcpretty piping when useXcpretty is set. Combined raw xcodebuild
// stdout/stderr is captured into the returned string for the existing
// log-parsing path; xcpretty consumes the same stdout for prettified terminal
// output.
func runWithRNWrap(buildCmd *xcodebuild.CommandBuilder, det wrap.Detection, useXcpretty bool) (string, error) {
	args := append([]string{"xcodebuild"}, buildCmd.CommandArgs()...)
	name, wrappedArgs := wrap.Wrap(det, args[0], args[1:])
	displayArgs := append([]string{name}, wrappedArgs...)

	util.LogWithTimestamp(colorstring.Green, "$ %s", strings.Join(displayArgs, " "))
	fmt.Println()

	var output bytes.Buffer
	xcCmd := exec.Command(name, wrappedArgs...) //nolint:gosec

	if !useXcpretty {
		xcCmd.Stdout = io.MultiWriter(os.Stdout, &output)
		xcCmd.Stderr = io.MultiWriter(os.Stderr, &output)

		return output.String(), xcCmd.Run()
	}

	// xcpretty pipeline: xcodebuild stdout → xcpretty stdin, while we tee the
	// raw output into `output` so callers can scan it for distribution-log
	// pointers etc. xcodebuild stderr also goes into `output` and to user stderr.
	xcprettyCmd := exec.Command("xcpretty") //nolint:gosec
	pr, pw := io.Pipe()
	xcCmd.Stdout = io.MultiWriter(pw, &output)
	xcCmd.Stderr = io.MultiWriter(os.Stderr, &output)
	xcprettyCmd.Stdin = pr
	xcprettyCmd.Stdout = os.Stdout
	xcprettyCmd.Stderr = os.Stderr

	if err := xcprettyCmd.Start(); err != nil {
		_ = pw.Close()

		return "", fmt.Errorf("start xcpretty: %w", err)
	}

	runErr := xcCmd.Run()
	_ = pw.Close()
	waitErr := xcprettyCmd.Wait()

	if runErr != nil {
		return output.String(), runErr
	}
	if waitErr != nil {
		return output.String(), waitErr
	}

	return output.String(), nil
}
