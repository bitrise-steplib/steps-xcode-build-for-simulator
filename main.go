package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bitrise-io/go-utils/colorstring"
	"github.com/bitrise-io/go-utils/command"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-io/go-utils/stringutil"
	"github.com/bitrise-io/steps-xcode-archive/utils"
	"github.com/bitrise-steplib/steps-xcode-archive-for-simulator/helper"
	"github.com/bitrise-tools/go-steputils/stepconf"
	"github.com/bitrise-tools/go-xcode/simulator"
	"github.com/bitrise-tools/go-xcode/xcodebuild"
	"github.com/bitrise-tools/go-xcode/xcpretty"
	shellquote "github.com/kballard/go-shellquote"
)

// xcodebuild build -workspace ~/Develop/XCode/XcodeArchiveTest/XcodeArchiveTest.xcworkspace -scheme Other -configuration Debug -sdk iphonesimulator11.4 -derivedDataPath ./ddata

const (
	minSupportedXcodeMajorVersion = 6
	iOSSimName                    = "iphonesimulator"
	tvOSSimName                   = "appletvsimulator"
)

const (
	bitriseXcodeRawResultTextEnvKey = "BITRISE_XCODE_RAW_RESULT_TEXT_PATH"
)

// Config ...
type Config struct {
	ProjectPath           string `env:"project_path,required"`
	Scheme                string `env:"scheme,required"`
	Configuration         string `env:"configuration"`
	ArtifactName          string `env:"artifact_name"`
	XcodebuildOptions     string `env:"xcodebuild_options"`
	Workdir               string `env:"workdir"`
	OutputDir             string `env:"output_dir,required"`
	IsCleanBuild          bool   `env:"is_clean_build,opt[yes,no]"`
	OutputTool            string `env:"output_tool,opt[xcpretty,xcodebuild]"`
	ForceCodeSignIdentity string `env:"force_code_sign_identity"`
	SimulatorDevice       string `env:"simulator_device,required"`
	SimulatorOsVersion    string `env:"simulator_os_version,required"`
	SimulatorPlatform     string `env:"simulator_platform,opt[iOS, tvOS]"`
	VerboseLog            bool   `env:"verbose_log,required"`
}

func main() {
	var cfg Config
	if err := stepconf.Parse(&cfg); err != nil {
		failf("Issue with input: %s", err)
	}

	log.SetEnableDebugLog(cfg.VerboseLog)

	stepconf.Print(cfg)
	fmt.Println()

	log.SetEnableDebugLog(cfg.VerboseLog)

	log.Infof("step determined configs:")

	_, err := helper.Ruby(cfg.ProjectPath, cfg.ArtifactName)
	if err != nil {
		failf("Ruby", err)
	}

	// Detect Xcode major version
	xcodebuildVersion, err := utils.XcodeBuildVersion()
	if err != nil {
		failf("Failed to determin xcode version, error: %s", err)
	}
	log.Printf("- xcodebuildVersion: %s (%s)", xcodebuildVersion.XcodeVersion.String(), xcodebuildVersion.BuildVersion)

	xcodeMajorVersion := xcodebuildVersion.XcodeVersion.Segments()[0]
	if xcodeMajorVersion < minSupportedXcodeMajorVersion {
		failf("Invalid xcode major version (%s), should not be less then min supported: %d", xcodeMajorVersion, minSupportedXcodeMajorVersion)
	}

	// Detect xcpretty version
	if cfg.OutputTool == "xcpretty" {
		if !utils.IsXcprettyInstalled() {
			failf(`xcpretty is not installed
	For xcpretty installation see: 'https://github.com/supermarin/xcpretty',
	or use 'xcodebuild' as 'output_tool'.`)
		}

		xcprettyVersion, err := utils.XcprettyVersion()
		if err != nil {
			failf("Failed to determin xcpretty version, error: %s", err)
		}
		log.Printf("- xcprettyVersion: %s", xcprettyVersion.String())
	}

	// abs out dir pth
	absOutputDir, err := pathutil.AbsPath(cfg.OutputDir)
	if err != nil {
		failf("Failed to expand OutputDir (%s), error: %s", cfg.OutputDir, err)
	}

	cfg.OutputDir = absOutputDir

	if exist, err := pathutil.IsPathExists(cfg.OutputDir); err != nil {
		failf("Failed to check if OutputDir exist, error: %s", err)
	} else if !exist {
		if err := os.MkdirAll(cfg.OutputDir, 0777); err != nil {
			failf("Failed to create OutputDir (%s), error: %s", cfg.OutputDir, err)
		}
	}

	// output files
	appPath := filepath.Join(cfg.OutputDir, cfg.ArtifactName+".app")
	rawXcodebuildOutputLogPath := filepath.Join(cfg.OutputDir, "raw-xcodebuild-output.log")

	archiveZipPath := filepath.Join(cfg.OutputDir, cfg.ArtifactName+".xcarchive.zip")
	ideDistributionLogsZipPath := filepath.Join(cfg.OutputDir, "xcodebuild.xcdistributionlogs.zip")

	// cleanup
	filesToCleanup := []string{
		appPath,
		rawXcodebuildOutputLogPath,

		archiveZipPath,
		ideDistributionLogsZipPath,
	}

	for _, pth := range filesToCleanup {
		if exist, err := pathutil.IsPathExists(pth); err != nil {
			failf("Failed to check if path (%s) exist, error: %s", pth, err)
		} else if exist {
			if err := os.RemoveAll(pth); err != nil {
				failf("Failed to remove path (%s), error: %s", pth, err)
			}
		}
	}

	//
	// Create the app with Xcode Command Line tools
	log.Infof("Runing build...")
	fmt.Println()

	isWorkspace := false
	ext := filepath.Ext(cfg.ProjectPath)
	if ext == ".xcodeproj" {
		isWorkspace = false
	} else if ext == ".xcworkspace" {
		isWorkspace = true
	} else {
		failf("Project file extension should be .xcodeproj or .xcworkspace, but got: %s", ext)
	}

	xcodeBuildCmd := xcodebuild.NewBuildCommand(cfg.ProjectPath, isWorkspace)
	xcodeBuildCmd.SetScheme(cfg.Scheme)
	xcodeBuildCmd.SetConfiguration(cfg.Configuration)

	// SDK version
	{
		simulatorVersion := cfg.SimulatorOsVersion
		if simulatorVersion == "latest" {
			// Simulator infos
			_, osVersion, err := simulator.GetLatestSimulatorInfoAndVersion(cfg.SimulatorPlatform, cfg.SimulatorDevice)
			if err != nil {
				failf("Failed to get latest simulator info - error: %s", err)
			}

			// format: `iphonesimulator11.4` or `appletvsimulator11.4`
			simulatorName := iOSSimName
			if cfg.SimulatorPlatform == "tvOS" {
				simulatorName = tvOSSimName
			}
			simulatorVersion = simulatorName + strings.TrimSpace(strings.TrimLeft(osVersion, cfg.SimulatorPlatform))
		}

		xcodeBuildCmd.SetSDK(simulatorVersion)
	}

	// Clean build
	if cfg.IsCleanBuild {
		xcodeBuildCmd.SetCustomBuildAction("clean")
	}

	// XcodeBuild Options
	if cfg.XcodebuildOptions != "" {
		options, err := shellquote.Split(cfg.XcodebuildOptions)
		if err != nil {
			failf("Failed to shell split XcodebuildOptions (%s), error: %s", cfg.XcodebuildOptions)
		}
		xcodeBuildCmd.SetCustomOptions(options)
	}

	// Output tool
	{
		if cfg.OutputTool == "xcpretty" {
			xcprettyCmd := xcpretty.New(xcodeBuildCmd)

			logWithTimestamp(colorstring.Green, "$ %s", xcprettyCmd.PrintableCmd())
			fmt.Println()

			if rawXcodebuildOut, err := xcprettyCmd.Run(); err != nil {
				log.Errorf("\nLast lines of the Xcode's build log:")
				fmt.Println(stringutil.LastNLines(rawXcodebuildOut, 10))

				if err := utils.ExportOutputFileContent(rawXcodebuildOut, rawXcodebuildOutputLogPath, bitriseXcodeRawResultTextEnvKey); err != nil {
					log.Warnf("Failed to export %s, error: %s", bitriseXcodeRawResultTextEnvKey, err)
				} else {
					log.Warnf(`You can find the last couple of lines of Xcode's build log above, but the full log is also available in the raw-xcodebuild-output.log
	The log file is stored in $BITRISE_DEPLOY_DIR, and its full path is available in the $BITRISE_XCODE_RAW_RESULT_TEXT_PATH environment variable
	(value: %s)`, rawXcodebuildOutputLogPath)
				}

				failf("Archive failed, error: %s", err)
			}
		} else {
			logWithTimestamp(colorstring.Green, "$ %s", xcodeBuildCmd.PrintableCmd())
			fmt.Println()

			archiveRootCmd := xcodeBuildCmd.Command()
			archiveRootCmd.SetStdout(os.Stdout)
			archiveRootCmd.SetStderr(os.Stderr)

			if err := archiveRootCmd.Run(); err != nil {
				failf("Archive failed, error: %s", err)
			}
		}
	}

	fmt.Println()

	// Copy app from Derived Data to Deploy dir
	{
		simulatorName := iOSSimName
		if cfg.SimulatorPlatform == "tvOS" {
			simulatorName = tvOSSimName
		}

		buildDir, err := targetBuildDir(cfg.ProjectPath, cfg.Scheme, cfg.Configuration, simulatorName, cfg.XcodebuildOptions)
		if err != nil {
			failf("Failed to get project's build target dir", err)
		}

		deployDir := os.Getenv("BITRISE_DEPLOY_DIR")
		source := filepath.Join(buildDir, cfg.ArtifactName+".app")
		destination := filepath.Join(deployDir, cfg.ArtifactName+".app")

		if err := copy(source, destination); err != nil {
			failf("Failed to copy the generated app to the Deploy dir")
		}
	}

	// Ensure app exists
	if exist, err := pathutil.IsPathExists(appPath); err != nil {
		failf("Failed to check if archive exist, error: %s", err)
	} else if !exist {
		failf("No app generated at: %s", appPath)
	}

}

func failf(format string, v ...interface{}) {
	log.Errorf(format, v...)
	os.Exit(1)
}

// ColoringFunc ...
type ColoringFunc func(...interface{}) string

func currentTimestamp() string {
	timeStampFormat := "15:04:05"
	currentTime := time.Now()
	return currentTime.Format(timeStampFormat)
}

func logWithTimestamp(coloringFunc ColoringFunc, format string, v ...interface{}) {
	message := fmt.Sprintf(format, v...)
	messageWithTimeStamp := fmt.Sprintf("[%s] %s", currentTimestamp(), coloringFunc(message))
	fmt.Println(messageWithTimeStamp)
}

func targetBuildDir(projectPath, scheme, configuration, sdk, buildOptions string) (string, error) {
	ext := filepath.Ext(projectPath)
	isWorkspace := false

	if ext == ".xcworkspace" {
		isWorkspace = true
	} else if ext != ".xcodeproj" {
		return "", fmt.Errorf("Project file extension should be .xcodeproj or .xcworkspace, but got: %s", ext)
	}

	xcodeBuildCmd := xcodebuild.NewBuildCommand(projectPath, isWorkspace)
	xcodeBuildCmd.SetCustomBuildAction("-showBuildSettings")
	xcodeBuildCmd.SetScheme(scheme)
	xcodeBuildCmd.SetConfiguration(configuration)
	xcodeBuildCmd.SetSDK(sdk)

	// XcodeBuild Options
	if buildOptions != "" {
		options, err := shellquote.Split(buildOptions)
		if err != nil {
			failf("Failed to shell split XcodebuildOptions (%s), error: %s", buildOptions)
		}
		xcodeBuildCmd.SetCustomOptions(options)
	}

	log.Printf(xcodeBuildCmd.PrintableCmd())

	output, err := xcodeBuildCmd.Command().RunAndReturnTrimmedOutput()
	if err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(bytes.NewBufferString(output))

	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "TARGET_BUILD_DIR") {
			dir := strings.TrimLeft(scanner.Text(), "TARGET_BUILD_DIR = ")
			return dir, nil
		}
	}

	return "", fmt.Errorf("could not find the project's target build dir")
}

func copy(source string, destination string) error {
	copyCmd := command.New("cp", "-R", source, destination)
	copyCmd.SetStdout(os.Stdout)
	copyCmd.SetStderr(os.Stderr)

	return copyCmd.Run()
}
