package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bitrise-io/go-steputils/stepconf"
	"github.com/bitrise-io/go-steputils/tools"
	"github.com/bitrise-io/go-utils/colorstring"
	"github.com/bitrise-io/go-utils/errorutil"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-io/go-utils/stringutil"
	"github.com/bitrise-io/go-xcode/simulator"
	"github.com/bitrise-io/go-xcode/utility"
	"github.com/bitrise-io/go-xcode/xcodebuild"
	"github.com/bitrise-io/go-xcode/xcpretty"
	"github.com/bitrise-io/xcode-project/serialized"
	"github.com/bitrise-io/xcode-project/xcodeproj"
	"github.com/bitrise-io/xcode-project/xcscheme"
	"github.com/bitrise-io/xcode-project/xcworkspace"
	"github.com/bitrise-steplib/steps-xcode-archive/utils"
	"github.com/bitrise-steplib/steps-xcode-build-for-simulator/util"
	shellquote "github.com/kballard/go-shellquote"
)

const (
	minSupportedXcodeMajorVersion = 7
	iOSSimName                    = "iphonesimulator"
	tvOSSimName                   = "appletvsimulator"
	watchOSSimName                = "watchsimulator"
)

const (
	bitriseXcodeRawResultTextEnvKey = "BITRISE_XCODE_RAW_RESULT_TEXT_PATH"
)

// Config ...
type Config struct {
	ProjectPath               string `env:"project_path,required"`
	Scheme                    string `env:"scheme,required"`
	Configuration             string `env:"configuration,required"`
	ArtifactName              string `env:"artifact_name"`
	XcodebuildOptions         string `env:"xcodebuild_options"`
	Workdir                   string `env:"workdir"`
	OutputDir                 string `env:"output_dir,required"`
	IsCleanBuild              bool   `env:"is_clean_build,opt[yes,no]"`
	OutputTool                string `env:"output_tool,opt[xcpretty,xcodebuild]"`
	SimulatorDevice           string `env:"simulator_device,required"`
	SimulatorOsVersion        string `env:"simulator_os_version,required"`
	SimulatorPlatform         string `env:"simulator_platform,opt[iOS,tvOS]"`
	DisableIndexWhileBuilding bool   `env:"disable_index_while_building,opt[yes,no]"`
	VerboseLog                bool   `env:"verbose_log,required"`
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

				if cmds, err := xcpretty.Install(); err != nil {
					log.Warnf("Failed to create xcpretty install command: %s", err)
					log.Warnf("Switching to xcodebuild for output tool")
					outputTool = "xcodebuild"
				} else {
					for _, cmd := range cmds {
						if out, err := cmd.RunAndReturnTrimmedCombinedOutput(); err != nil {
							if errorutil.IsExitStatusError(err) {
								log.Warnf("%s failed: %s", out)
							} else {
								log.Warnf("%s failed: %s", err)
							}
							log.Warnf("Switching to xcodebuild for output tool")
							outputTool = "xcodebuild"
						}
					}
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
			if err := os.RemoveAll(pth); err != nil {
				failf("Failed to remove path (%s), error: %s", pth, err)
			}

		}
	}

	//
	// Get simulator info from the provided OS, platform and device
	var simulatorID string
	{
		fmt.Println()
		log.Infof("Simulator info")

		// Simulator Destination
		simulatorID, err = simulatorDestinationID(cfg.SimulatorOsVersion, cfg.SimulatorPlatform, cfg.SimulatorDevice)
		if err != nil {
			failf("Failed to find simulator, error: %s", err)
		}
	}

	//
	// Create the app with Xcode Command Line tools
	{
		fmt.Println()
		log.Infof("Running build")

		var isWorkspace bool
		if xcworkspace.IsWorkspace(cfg.ProjectPath) {
			isWorkspace = true
		} else if !xcodeproj.IsXcodeProj(cfg.ProjectPath) {
			failf("Project file extension should be .xcodeproj or .xcworkspace, but got: %s", filepath.Ext(cfg.ProjectPath))
		}

		// Build for simulator command
		xcodeBuildCmd := xcodebuild.NewCommandBuilder(cfg.ProjectPath, isWorkspace, xcodebuild.BuildAction)
		xcodeBuildCmd.SetScheme(cfg.Scheme)
		xcodeBuildCmd.SetConfiguration(cfg.Configuration)

		// Disable the code signing for simulator build
		xcodeBuildCmd.SetDisableCodesign(true)

		// Set simulator destination and disable code signing for the build
		xcodeBuildCmd.SetDestination("id=" + simulatorID)

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

		// Disabe indexing while building
		xcodeBuildCmd.SetDisableIndexWhileBuilding(cfg.DisableIndexWhileBuilding)

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
	var exportedArtifacts []string
	{
		fmt.Println()
		log.Infof("Copy artifacts from Derived Data to %s", absOutputDir)

		proj, _, err := findBuiltProject(cfg.ProjectPath, cfg.Scheme, cfg.Configuration)
		if err != nil {
			failf("Failed to open xcproj - (%s), error:", cfg.ProjectPath, err)
		}

		customOptions, err := shellquote.Split(cfg.XcodebuildOptions)
		if err != nil {
			failf("Failed to shell split XcodebuildOptions (%s), error: %s", cfg.XcodebuildOptions)
		}

		// Get the simulator name
		{
			simulatorName := iOSSimName
			if cfg.SimulatorPlatform == "tvOS" {
				simulatorName = tvOSSimName
			}

			customOptions = append(customOptions, "-sdk")
			customOptions = append(customOptions, simulatorName)
		}

		schemeBuildDir, err := buildTargetDirForScheme(proj, cfg.ProjectPath, cfg.Scheme, cfg.Configuration, customOptions...)
		if err != nil {
			failf("Failed to get scheme (%s) build target dir, error: %s", err)
		}

		log.Debugf("Scheme build dir: %s", schemeBuildDir)

		// Export the artifact from the build dir to the output_dir
		if exportedArtifacts, err = exportArtifacts(proj, cfg.Scheme, schemeBuildDir, cfg.Configuration, cfg.SimulatorPlatform, absOutputDir); err != nil {
			failf("Failed to export the artifacts, error: %s", err)
		}
	}

	//
	// Export output
	fmt.Println()
	log.Infof("Exporting outputs")
	if len(exportedArtifacts) == 0 {
		log.Warnf("No exportable artifact have found.")
	} else {
		mainTargetAppPath, pathMap, err := exportOutput(exportedArtifacts)
		if err != nil {
			failf("Failed to export outputs (BITRISE_APP_DIR_PATH & BITRISE_APP_DIR_PATH_LIST), error: %s", err)
		}

		log.Donef("BITRISE_APP_DIR_PATH -> %s", mainTargetAppPath)
		log.Donef("BITRISE_APP_DIR_PATH_LIST -> %s", pathMap)

		fmt.Println()
		log.Donef("You can find the exported artifacts in: %s", absOutputDir)
	}
}

func exportOutput(artifacts []string) (string, string, error) {
	if err := tools.ExportEnvironmentWithEnvman("BITRISE_APP_DIR_PATH", artifacts[0]); err != nil {
		return "", "", err
	}

	pathMap := strings.Join(artifacts, "|")
	pathMap = strings.Trim(pathMap, "|")
	if err := tools.ExportEnvironmentWithEnvman("BITRISE_APP_DIR_PATH_LIST", pathMap); err != nil {
		return "", "", err
	}
	return artifacts[0], pathMap, nil
}

// findBuiltProject returns the Xcode project which will be built for the provided scheme
func findBuiltProject(pth, schemeName, configurationName string) (xcodeproj.XcodeProj, string, error) {
	var scheme xcscheme.Scheme
	var schemeContainerDir string

	if xcodeproj.IsXcodeProj(pth) {
		project, err := xcodeproj.Open(pth)
		if err != nil {
			return xcodeproj.XcodeProj{}, "", err
		}

		var ok bool
		scheme, ok = project.Scheme(schemeName)
		if !ok {
			return xcodeproj.XcodeProj{}, "", fmt.Errorf("no scheme found with name: %s in project: %s", schemeName, pth)
		}
		schemeContainerDir = filepath.Dir(pth)
	} else if xcworkspace.IsWorkspace(pth) {
		workspace, err := xcworkspace.Open(pth)
		if err != nil {
			return xcodeproj.XcodeProj{}, "", err
		}

		var containerProject string
		scheme, containerProject, err = workspace.Scheme(schemeName)
		if err != nil {
			return xcodeproj.XcodeProj{}, "", fmt.Errorf("no scheme found with name: %s in workspace: %s, error: %s", schemeName, pth, err)
		}
		schemeContainerDir = filepath.Dir(containerProject)
	} else {
		return xcodeproj.XcodeProj{}, "", fmt.Errorf("unknown project extension: %s", filepath.Ext(pth))
	}

	if configurationName == "" {
		configurationName = scheme.ArchiveAction.BuildConfiguration
	}

	if configurationName == "" {
		return xcodeproj.XcodeProj{}, "", fmt.Errorf("no configuration provided nor default defined for the scheme's (%s) archive action", schemeName)
	}

	var archiveEntry xcscheme.BuildActionEntry
	for _, entry := range scheme.BuildAction.BuildActionEntries {
		if entry.BuildForArchiving != "YES" || !entry.BuildableReference.IsAppReference() {
			continue
		}
		archiveEntry = entry
		break
	}

	if archiveEntry.BuildableReference.BlueprintIdentifier == "" {
		return xcodeproj.XcodeProj{}, "", fmt.Errorf("archivable entry not found")
	}

	projectPth, err := archiveEntry.BuildableReference.ReferencedContainerAbsPath(schemeContainerDir)
	if err != nil {
		return xcodeproj.XcodeProj{}, "", err
	}

	project, err := xcodeproj.Open(projectPth)
	if err != nil {
		return xcodeproj.XcodeProj{}, "", err
	}

	return project, scheme.Name, nil
}

// buildTargetDirForScheme returns the TARGET_BUILD_DIR for the provided scheme
func buildTargetDirForScheme(proj xcodeproj.XcodeProj, projectPath, scheme, configuration string, customOptions ...string) (string, error) {
	// Fetch project's main target from .xcodeproject
	var buildSettings serialized.Object
	if xcodeproj.IsXcodeProj(projectPath) {
		mainTarget, err := mainTargetOfScheme(proj, scheme)
		if err != nil {
			return "", fmt.Errorf("failed to fetch project's targets, error: %s", err)
		}

		buildSettings, err = proj.TargetBuildSettings(mainTarget.Name, configuration, customOptions...)
		if err != nil {
			return "", fmt.Errorf("failed to parse project (%s) build settings, error: %s", projectPath, err)
		}
	} else if xcworkspace.IsWorkspace(projectPath) {
		workspace, err := xcworkspace.Open(projectPath)
		if err != nil {
			return "", fmt.Errorf("Failed to open xcworkspace (%s), error: %s", projectPath, err)
		}

		buildSettings, err = workspace.SchemeBuildSettings(scheme, configuration, customOptions...)
		if err != nil {
			return "", fmt.Errorf("failed to parse workspace (%s) build settings, error: %s", projectPath, err)
		}
	} else {
		return "", fmt.Errorf("project file extension should be .xcodeproj or .xcworkspace, but got: %s", filepath.Ext(projectPath))

	}

	schemeBuildDir, err := buildSettings.String("TARGET_BUILD_DIR")
	if err != nil {
		return "", fmt.Errorf("failed to parse build settings, error: %s", err)
	}

	return schemeBuildDir, nil
}

// exportArtifacts exports the main target and it's .app dependencies.
func exportArtifacts(proj xcodeproj.XcodeProj, scheme string, schemeBuildDir string, configuration, simulatorPlatform, deployDir string, customOptions ...string) ([]string, error) {
	var exportedArtifacts []string
	splitSchemeDir := strings.Split(schemeBuildDir, "Build/")
	var schemeDir string

	// Split the scheme's TARGET_BUILD_DIR by the BUILD dir. This path will be the base path for the targets's TARGET_BUILD_DIR
	//
	// xcodebuild -showBuildSettings will produce different outputs if you call it with a -workspace & -scheme or if you call it with a -project & -target.
	// We need to call xcodebuild -showBuildSettings for all of the project targets to find the build artifacts (iOS, watchOS etc...)
	if len(splitSchemeDir) != 2 {
		log.Debugf("failed to parse scheme's build target dir: %s. Using the original build dir (%s)\n", schemeBuildDir, schemeBuildDir)
		schemeDir = schemeBuildDir
	} else {
		schemeDir = filepath.Join(splitSchemeDir[0], "Build")
	}

	mainTarget, err := mainTargetOfScheme(proj, scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch project's targets, error: %s", err)
	}

	targets := append([]xcodeproj.Target{mainTarget}, mainTarget.DependentTargets()...)

	for _, target := range targets {
		log.Donef(target.Name + "...")

		// Is the target an application? -> If not skip the export
		if !strings.HasSuffix(target.ProductReference.Path, ".app") {
			log.Printf("Target (%s) is not an .app - SKIP", target.Name)
			continue
		}

		//
		// Find out the sdk for the target
		simulatorName := iOSSimName
		if simulatorPlatform == "tvOS" {
			simulatorName = tvOSSimName
		}
		{

			settings, err := proj.TargetBuildSettings(target.Name, configuration)
			if err != nil {
				log.Debugf("Failed to fetch project settings (%s), error: %s", proj.Path, err)
			}

			sdkRoot, err := settings.String("SDKROOT")
			if err != nil {
				log.Debugf("No SDKROOT config found for (%s) target", target.Name)
			}

			log.Debugf("sdkRoot: %s", sdkRoot)

			if strings.Contains(sdkRoot, "WatchOS.platform") {
				simulatorName = watchOSSimName
			}
		}

		//
		// Find the TARGET_BUILD_DIR for the target
		var targetDir string
		{
			customOptions = []string{"-sdk", simulatorName}
			buildSettings, err := proj.TargetBuildSettings(target.Name, configuration, customOptions...)
			if err != nil {
				return nil, fmt.Errorf("failed to get project build settings, error: %s", err)
			}

			buildDir, err := buildSettings.String("TARGET_BUILD_DIR")
			if err != nil {
				return nil, fmt.Errorf("failed to get build target dir for target (%s), error: %s", target.Name, err)
			}

			log.Debugf("Target (%s) TARGET_BUILD_DIR: %s", target.Name, buildDir)

			// Split the target's TARGET_BUILD_DIR by the BUILD dir. This path will be joined to the `schemeBuildDir`
			//
			// xcodebuild -showBuildSettings will produce different outputs if you call it with a -workspace & -scheme or if you call it with a -project & -target.
			// We need to call xcodebuild -showBuildSettings for all of the project targets to find the build artifacts (iOS, watchOS etc...)
			splitTargetDir := strings.Split(buildDir, "Build/")
			if len(splitTargetDir) != 2 {
				log.Debugf("failed to parse build target dir (%s) for target: %s. Using the original build dir (%s)\n", buildDir, target.Name, buildDir)
				targetDir = buildDir
			} else {
				targetDir = splitTargetDir[1]
			}

		}

		//
		// Copy - export
		{

			// Search for the generated build artifact in the next dirs:
			// Parent dir (main target's build dir by the provided scheme) + current target's build dir (This is a default for a nativ iOS project)
			// current target's build dir (If the project settings uses a custom TARGET_BUILD_DIR env)
			// .xcodeproj's directory + current target's build dir (If the project settings uses a custom TARGET_BUILD_DIR env & the project is not in the root dir)
			sourceDirs := []string{filepath.Join(schemeDir, targetDir), schemeDir, filepath.Join(path.Dir(proj.Path), schemeDir)}
			destination := filepath.Join(deployDir, target.ProductReference.Path)

			// Search for the generated build artifact
			var exported bool
			for _, sourceDir := range sourceDirs {
				source := filepath.Join(sourceDir, target.ProductReference.Path)
				log.Debugf("searching for the generated app in %s", source)

				if exists, err := pathutil.IsPathExists(source); err != nil {
					log.Debugf("failed to check if the path exists: (%s), error: ", source, err)
					continue

				} else if !exists {
					log.Debugf("path not exists: %s", source)
					// Also check to see if a path exists with the target name
					source := filepath.Join(sourceDir, strings.Join(target.Name, ".app"))

					if exists, err := pathutil.IsPathExists(source); err != nil {
						log.Debugf("failed to check if the path exists: (%s), error: ", source, err)
						continue
					} else if !exists {
						continue
					}
				}

				// Copy the build artifact
				cmd := util.CopyDir(source, destination)
				cmd.SetStdout(os.Stdout)
				cmd.SetStderr(os.Stderr)
				log.Debugf("$ " + cmd.PrintableCommandArgs())
				if err := cmd.Run(); err != nil {
					log.Debugf("failed to copy the generated app from (%s) to the Deploy dir\n", source)
					continue
				}

				exported = true
				break
			}

			if exported {
				exportedArtifacts = append(exportedArtifacts, destination)
				log.Debugf("Success\n")
			} else {
				return nil, fmt.Errorf("failed to copy the generated app to the Deploy dir")
			}
		}
	}

	return exportedArtifacts, nil
}

// mainTargetOfScheme return the main target
func mainTargetOfScheme(proj xcodeproj.XcodeProj, scheme string) (xcodeproj.Target, error) {
	projTargets := proj.Proj.Targets
	sch, ok := proj.Scheme(scheme)
	if !ok {
		return xcodeproj.Target{}, fmt.Errorf("Failed to found scheme (%s) in project", scheme)
	}

	var blueIdent string
	for _, entry := range sch.BuildAction.BuildActionEntries {
		if entry.BuildableReference.IsAppReference() {
			blueIdent = entry.BuildableReference.BlueprintIdentifier
			break
		}
	}

	// Search for the main target
	for _, t := range projTargets {
		if t.ID == blueIdent {
			return t, nil

		}
	}
	return xcodeproj.Target{}, fmt.Errorf("failed to find the project's main target for scheme (%s)", scheme)
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
