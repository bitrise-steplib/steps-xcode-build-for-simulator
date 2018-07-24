package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bitrise-io/go-utils/colorstring"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-io/go-utils/stringutil"
	"github.com/bitrise-io/steps-xcode-analyze/utils"
	"github.com/bitrise-io/steps-xcode-test/pretty"
	"github.com/bitrise-steplib/steps-xcode-archive-for-simulator/util"
	"github.com/bitrise-tools/go-steputils/stepconf"
	"github.com/bitrise-tools/go-xcode/simulator"
	"github.com/bitrise-tools/go-xcode/utility"
	"github.com/bitrise-tools/go-xcode/xcodebuild"
	"github.com/bitrise-tools/go-xcode/xcpretty"
	"github.com/bitrise-tools/xcode-project/xcodeproj"
	shellquote "github.com/kballard/go-shellquote"
)

const (
	minSupportedXcodeMajorVersion = 6
	iOSSimName                    = "iphonesimulator"
	tvOSSimName                   = "appletvsimulator"
	watchOSSimName                = "watchsimulator"
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

	log.Infof("Step determined configs:")

	// Detect Xcode major version
	xcodebuildVersion, err := utility.GetXcodeVersion()
	if err != nil {
		failf("Failed to determin xcode version, error: %s", err)
	}
	log.Printf("- xcodebuildVersion: %s (%s)", xcodebuildVersion.Version, xcodebuildVersion.BuildVersion)

	xcodeMajorVersion := xcodebuildVersion.MajorVersion
	if xcodeMajorVersion < minSupportedXcodeMajorVersion {
		failf("Invalid xcode major version (%d), should not be less then min supported: %d", xcodeMajorVersion, minSupportedXcodeMajorVersion)
	}

	// Detect xcpretty version
	outputTool := cfg.OutputTool
	if outputTool == "xcpretty" {
		fmt.Println()
		log.Infof("Checking if output tool (xcpretty) is installed")

		installed, err := xcpretty.IsInstalled()
		if err != nil {
			log.Warnf("Failed to check if xcpretty is installed, error: %s", err)
			log.Printf("Switching to xcodebuild for output tool")
			outputTool = "xcodebuild"
		} else if !installed {
			log.Warnf(`xcpretty is not installed`)
			fmt.Println()
			log.Printf("Installing xcpretty")

			if err := xcpretty.Install(); err != nil {
				log.Warnf("Failed to install xcpretty, error: %s", err)
				log.Printf("Switching to xcodebuild for output tool")
				outputTool = "xcodebuild"
			}
		}
	}

	if outputTool == "xcpretty" {
		xcprettyVersion, err := xcpretty.Version()
		if err != nil {
			log.Warnf("Failed to determin xcpretty version, error: %s", err)
			log.Printf("Switching to xcodebuild for output tool")
			outputTool = "xcodebuild"
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
	rawXcodebuildOutputLogPath := filepath.Join(cfg.OutputDir, "raw-xcodebuild-output.log")

	// cleanup
	{
		filesToCleanup := []string{
			rawXcodebuildOutputLogPath,
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
	}

	//
	// Create the app with Xcode Command Line tools
	{
		isWorkspace := false
		ext := filepath.Ext(cfg.ProjectPath)
		if ext == ".xcodeproj" {
			isWorkspace = false
		} else if ext == ".xcworkspace" {
			isWorkspace = true
		} else {
			failf("Project file extension should be .xcodeproj or .xcworkspace, but got: %s", ext)
		}

		xcodeBuildCmd := xcodebuild.NewCommandBuilder(cfg.ProjectPath, isWorkspace, xcodebuild.BuildAction)
		xcodeBuildCmd.SetScheme(cfg.Scheme)
		xcodeBuildCmd.SetConfiguration(cfg.Configuration)

		fmt.Println()
		log.Infof("Simulator info")

		// Simulator Destination
		var simulatorID string
		{
			simulatorVersion := cfg.SimulatorOsVersion

			if simulatorVersion == "latest" {
				info, _, err := simulator.GetLatestSimulatorInfoAndVersion(cfg.SimulatorPlatform, cfg.SimulatorDevice)
				if err != nil {
					failf("Failed to get latest simulator info - error: %s", err)
				}

				simulatorID = info.ID
				log.Printf("Latest simulator for %s = %s", cfg.SimulatorDevice, simulatorID)
			} else {
				info, err := simulator.GetSimulatorInfo((cfg.SimulatorPlatform + " " + cfg.SimulatorOsVersion), cfg.SimulatorDevice)
				if err != nil {
					failf("Failed to get simulator info (%s-%s) - error: %s", (cfg.SimulatorPlatform + cfg.SimulatorOsVersion), cfg.SimulatorDevice, err)
				}

				simulatorID = info.ID
				log.Printf("Simulator for %s %s = %s", cfg.SimulatorDevice, cfg.SimulatorOsVersion, simulatorID)
			}
		}

		// Set simulator destination and disable code signing for the build
		xcodeBuildCmd.SetCustomOptions([]string{"-destination", "id=" + simulatorID, "PROVISIONING_PROFILE_SPECIFIER="})

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

		fmt.Println()
		log.Infof("Running build")

		// Output tool
		{
			if cfg.OutputTool == "xcpretty" {
				xcprettyCmd := xcpretty.New(xcodeBuildCmd)

				util.LogWithTimestamp(colorstring.Green, "$ %s", xcprettyCmd.PrintableCmd())
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
				util.LogWithTimestamp(colorstring.Green, "$ %s", xcodeBuildCmd.PrintableCmd())
				fmt.Println()

				buildRootCmd := xcodeBuildCmd.Command()
				buildRootCmd.SetStdout(os.Stdout)
				buildRootCmd.SetStderr(os.Stderr)

				if err := buildRootCmd.Run(); err != nil {
					failf("Archive failed, error: %s", err)
				}
			}
		}
	}

	fmt.Println()
	log.Infof("Copy artifacts from Derived Data to %s", cfg.OutputDir)
	{
		// Fetch project's targets from .xcodeproject
		targets, mainTarget, err := buildedTargets(cfg.ProjectPath, cfg.Scheme)
		if err != nil {
			failf("Failed to fetch project's targets, error: %s", err)
		}

		// Export the artifact from the build dir to the output_dir
		if err := exportArtifacts(targets, mainTarget, cfg.ProjectPath, cfg.Configuration, cfg.XcodebuildOptions, cfg.SimulatorPlatform); err != nil {
			failf("Failed to export the artifacts, error: %s", err)
		}
	}
}

func targetBuildDir(projectPath, targetName, configuration, sdk, buildOptions string) (string, error) {
	ext := filepath.Ext(projectPath)
	isWorkspace := false

	if ext == ".xcworkspace" {
		isWorkspace = true
	} else if ext != ".xcodeproj" {
		return "", fmt.Errorf("Project file extension should be .xcodeproj or .xcworkspace, but got: %s", ext)
	}

	xcodeBuildCmd := xcodebuild.NewCommandBuilder(projectPath, isWorkspace, xcodebuild.BuildAction)
	xcodeBuildCmd.SetCustomBuildAction("-showBuildSettings")
	xcodeBuildCmd.SetCustomOptions([]string{"-target", targetName})
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

func exportArtifacts(targets []xcodeproj.Target, mainTarget xcodeproj.Target, projectPath, configuration, XcodebuildOptions, simulatorPlatform string) error {
	for _, target := range targets {
		simulatorName := iOSSimName

		if target.ID != mainTarget.ID {
			sdkRoot, err := target.BuildConfigurationList.BuildConfigurations[0].BuildSettings.Value("SDKROOT")
			if err != nil {
				continue
			}

			_, err = target.BuildConfigurationList.BuildConfigurations[0].BuildSettings.Value("ASSETCATALOG_COMPILER_APPICON_NAME")
			if err != nil {
				continue
			}

			if sdkRoot == "watchos" {
				simulatorName = watchOSSimName
			}
		} else {
			if simulatorPlatform == "tvOS" {
				simulatorName = tvOSSimName
			}
		}

		log.Warnf(target.Name + ":")
		buildDir, err := targetBuildDir(projectPath, target.Name, configuration, simulatorName, XcodebuildOptions)
		if err != nil {
			return fmt.Errorf("failed to get project's build target dir, error: %s", err)
		}

		deployDir := os.Getenv("BITRISE_DEPLOY_DIR")
		source := filepath.Join(buildDir, target.Name+".app")
		destination := filepath.Join(deployDir, target.Name+".app")

		if err := util.CopyDir(source, destination); err != nil {
			return fmt.Errorf("failed to copy the generated app to the Deploy dir")
		}

		fmt.Println()
	}

	return nil
}

func buildedTargets(projectPath, scheme string) ([]xcodeproj.Target, xcodeproj.Target, error) {
	//
	// Project targets
	var targets []xcodeproj.Target
	var mainTarget xcodeproj.Target
	{
		proj, err := xcodeproj.Open(projectPath)
		if err != nil {
			return nil, xcodeproj.Target{}, fmt.Errorf("Failed to open xcproj - (%s), error: %s", projectPath, err)
		}

		projTargets := proj.Proj.Targets
		scheme, ok := proj.Scheme(scheme)
		if !ok {
			return nil, xcodeproj.Target{}, fmt.Errorf("Failed to found scheme (%s) in project", scheme)
		}

		blueIdent := scheme.BuildAction.BuildActionEntries[0].BuildableReference.BlueprintIdentifier

		for _, p := range projTargets {
			if p.ID == blueIdent {
				mainTarget = p
				targets = append(targets, mainTarget)
			}
		}
		log.Debugf("Project's targets: %+v\n", pretty.Object(projTargets))
		log.Debugf("Main target: %+v\n", pretty.Object(mainTarget))
	}

	// Main target's depenencies
	for _, dep := range mainTarget.DependentTargets() {
		targets = append(targets, dep)
	}

	return targets, mainTarget, nil
}

func failf(format string, v ...interface{}) {
	log.Errorf(format, v...)
	os.Exit(1)
}
