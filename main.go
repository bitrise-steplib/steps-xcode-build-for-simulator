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
	"github.com/bitrise-steplib/steps-xcode-build-for-simulator/util"
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
	ProjectPath        string `env:"project_path,required"`
	Scheme             string `env:"scheme,required"`
	Configuration      string `env:"configuration"`
	ArtifactName       string `env:"artifact_name"`
	XcodebuildOptions  string `env:"xcodebuild_options"`
	Workdir            string `env:"workdir"`
	OutputDir          string `env:"output_dir,required"`
	IsCleanBuild       bool   `env:"is_clean_build,opt[yes,no]"`
	OutputTool         string `env:"output_tool,opt[xcpretty,xcodebuild]"`
	SimulatorDevice    string `env:"simulator_device,required"`
	SimulatorOsVersion string `env:"simulator_os_version,required"`
	SimulatorPlatform  string `env:"simulator_platform,opt[iOS, tvOS]"`
	VerboseLog         bool   `env:"verbose_log,required"`
}

func main() {
	//
	// Config
	var cfg Config
	if err := stepconf.Parse(&cfg); err != nil {
		failf("Issue with input: %s", err)
	}

	stepconf.Print(cfg)
	fmt.Println()

	log.SetEnableDebugLog(cfg.VerboseLog)

	//
	// Determined configs
	{
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
	}

	// ABS out dir pth
	absOutputDir, err := pathutil.AbsPath(cfg.OutputDir)
	if err != nil {
		failf("Failed to expand OutputDir (%s), error: %s", cfg.OutputDir, err)
	}

	if exist, err := pathutil.IsPathExists(absOutputDir); err != nil {
		failf("Failed to check if OutputDir exist, error: %s", err)
	} else if !exist {
		if err := os.MkdirAll(absOutputDir, 0777); err != nil {
			failf("Failed to create OutputDir (%s), error: %s", absOutputDir, err)
		}
	}

	// Output files
	rawXcodebuildOutputLogPath := filepath.Join(absOutputDir, "raw-xcodebuild-output.log")

	//
	// Cleanup
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

		// Build for simulator command
		xcodeBuildCmd := xcodebuild.NewCommandBuilder(cfg.ProjectPath, isWorkspace, xcodebuild.BuildAction)
		xcodeBuildCmd.SetScheme(cfg.Scheme)
		xcodeBuildCmd.SetConfiguration(cfg.Configuration)

		fmt.Println()
		log.Infof("Simulator info")

		// Simulator Destination
		simulatorID, err := simulatorDestinationID(cfg.SimulatorOsVersion, cfg.SimulatorPlatform, cfg.SimulatorDevice)
		if err != nil {
			failf("Failed to find simulator, error: %s", err)
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

					failf("Build failed, error: %s", err)
				}
			} else {
				util.LogWithTimestamp(colorstring.Green, "$ %s", xcodeBuildCmd.PrintableCmd())
				fmt.Println()

				buildRootCmd := xcodeBuildCmd.Command()
				buildRootCmd.SetStdout(os.Stdout)
				buildRootCmd.SetStderr(os.Stderr)

				if err := buildRootCmd.Run(); err != nil {
					failf("Build failed, error: %s", err)
				}
			}
		}
	}

	//
	// Export artifacts
	{
		fmt.Println()
		log.Infof("Copy artifacts from Derived Data to %s", absOutputDir)

		// Fetch project's targets from .xcodeproject
		targets, mainTarget, err := schemeTargets(cfg.ProjectPath, cfg.Scheme)
		if err != nil {
			failf("Failed to fetch project's targets, error: %s", err)
		}

		// Export the artifact from the build dir to the output_dir
		if err := exportArtifacts(targets, mainTarget, cfg.ProjectPath, cfg.Configuration, cfg.XcodebuildOptions, cfg.SimulatorPlatform, absOutputDir); err != nil {
			failf("Failed to export the artifacts, error: %s", err)
		}
	}

	fmt.Println()
	log.Donef("You can find the exported artifacts in: %s", absOutputDir)
}

// targetBuildDir returns the target's TARGET_BUILD_DIR path for the provided sdk (e.g iossimulator)
func targetBuildDir(projectPath, targetName, configuration, sdk, buildOptions string) (string, error) {

	projPth := strings.Replace(projectPath, ".xcworkspace", ".xcodeproj", 1)

	xcodeBuildCmd := xcodebuild.NewCommandBuilder(projPth, false, xcodebuild.BuildAction)
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

// exportArtifacts exports the main target and it's .app dependencies.
func exportArtifacts(targets []xcodeproj.Target, mainTarget xcodeproj.Target, projectPath, configuration, XcodebuildOptions, simulatorPlatform, deployDir string) error {
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

		log.Donef(target.Name + "...")
		buildDir, err := targetBuildDir(projectPath, target.Name, configuration, simulatorName, XcodebuildOptions)
		if err != nil {
			return fmt.Errorf("failed to get project's build target dir, error: %s", err)
		}

		source := filepath.Join(buildDir, target.Name+".app")
		destination := filepath.Join(deployDir, target.Name+".app")

		if err := util.CopyDir(source, destination); err != nil {
			return fmt.Errorf("failed to copy the generated app to the Deploy dir")
		}

		fmt.Println()
	}

	return nil
}

// schemeTargets return the main target and it's dependent .app targets for the provided scheme.
func schemeTargets(projectPath, scheme string) ([]xcodeproj.Target, xcodeproj.Target, error) {
	projPth := strings.Replace(projectPath, ".xcworkspace", ".xcodeproj", 1)

	var targets []xcodeproj.Target
	var mainTarget xcodeproj.Target
	{
		proj, err := xcodeproj.Open(projPth)
		if err != nil {
			return nil, xcodeproj.Target{}, fmt.Errorf("Failed to open xcproj - (%s), error: %s", projPth, err)
		}

		projTargets := proj.Proj.Targets
		scheme, ok := proj.Scheme(scheme)
		if !ok {
			return nil, xcodeproj.Target{}, fmt.Errorf("Failed to found scheme (%s) in project", scheme)
		}

		blueIdent := scheme.BuildAction.BuildActionEntries[0].BuildableReference.BlueprintIdentifier

		// Search for the main target
		for _, t := range projTargets {
			if t.ID == blueIdent {
				mainTarget = t
				targets = append(targets, mainTarget)
				break
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

// simulatorDestinationID return the simulator's ID for the selected device version.
func simulatorDestinationID(simulatorOsVersion, simulatorPlatform, simulatorDevice string) (string, error) {
	var simulatorID string

	if simulatorOsVersion == "latest" {
		info, _, err := simulator.GetLatestSimulatorInfoAndVersion(simulatorPlatform, simulatorDevice)
		if err != nil {
			return "", fmt.Errorf("failed to get latest simulator info - error: %s", err)
		}

		simulatorID = info.ID
		log.Printf("Latest simulator for %s = %s", simulatorDevice, simulatorID)
	} else {
		info, err := simulator.GetSimulatorInfo((simulatorPlatform + " " + simulatorOsVersion), simulatorDevice)
		if err != nil {
			return "", fmt.Errorf("failed to get simulator info (%s-%s) - error: %s", (simulatorPlatform + simulatorOsVersion), simulatorDevice, err)
		}

		simulatorID = info.ID
		log.Printf("Simulator for %s %s = %s", simulatorDevice, simulatorOsVersion, simulatorID)
	}
	return simulatorID, nil
}

func failf(format string, v ...interface{}) {
	log.Errorf(format, v...)
	os.Exit(1)
}
