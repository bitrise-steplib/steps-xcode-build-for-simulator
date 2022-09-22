package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bitrise-io/go-steputils/stepconf"
	"github.com/bitrise-io/go-steputils/tools"
	"github.com/bitrise-io/go-utils/errorutil"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-io/go-utils/sliceutil"
	"github.com/bitrise-io/go-utils/stringutil"
	"github.com/bitrise-io/go-xcode/utility"
	"github.com/bitrise-io/go-xcode/xcodebuild"
	cache "github.com/bitrise-io/go-xcode/xcodecache"
	"github.com/bitrise-io/go-xcode/xcpretty"
	"github.com/bitrise-io/xcode-project/serialized"
	"github.com/bitrise-io/xcode-project/xcodeproj"
	"github.com/bitrise-io/xcode-project/xcscheme"
	"github.com/bitrise-io/xcode-project/xcworkspace"
	"github.com/bitrise-steplib/steps-xcode-archive/utils"
	"github.com/bitrise-steplib/steps-xcode-build-for-simulator/util"
	"github.com/kballard/go-shellquote"
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
	Configuration             string `env:"configuration"`
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
	CodeSigningAllowed        bool   `env:"code_signing_allowed,opt[yes,no]"`
	VerboseLog                bool   `env:"verbose_log,required"`
	CacheLevel                string `env:"cache_level,opt[none,swift_packages]"`
}

// BuildForSimulatorStep ...
type BuildForSimulatorStep struct{}

// NewBuildForSimulatorStep ...
func NewBuildForSimulatorStep() BuildForSimulatorStep {
	return BuildForSimulatorStep{}
}

// ProcessConfig ...
func (b BuildForSimulatorStep) ProcessConfig() (Config, error) {
	var cfg Config
	err := stepconf.Parse(&cfg)
	if err != nil {
		return cfg, fmt.Errorf("unable to parse input: %s", err)
	}

	log.SetEnableDebugLog(cfg.VerboseLog)
	stepconf.Print(cfg)
	fmt.Println()

	return cfg, nil
}

// InstallDependencies ...
func (b BuildForSimulatorStep) InstallDependencies(cfg Config) (Config, error) {
	if cfg.OutputTool != "xcpretty" {
		return cfg, nil
	}

	outputTool := cfg.OutputTool

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
	xcprettyVersion, err := xcpretty.Version()
	if err != nil {
		log.Warnf("Failed to determine xcpretty version, error: %s", err)
		log.Printf("Switching to xcodebuild for output tool")
		outputTool = "xcodebuild"
	}
	log.Printf("- xcprettyVersion: %s", xcprettyVersion.String())

	cfg.OutputTool = outputTool
	return cfg, nil
}

// Run ...
func (b BuildForSimulatorStep) Run(cfg Config) (ExportOptions, error) {
	// Detect Xcode major version
	xcodebuildVersion, err := utility.GetXcodeVersion()
	if err != nil {
		return ExportOptions{}, fmt.Errorf("failed to determine xcode version, error: %s", err)
	}
	log.Printf("- xcodebuildVersion: %s (%s)", xcodebuildVersion.Version, xcodebuildVersion.BuildVersion)

	xcodeMajorVersion := xcodebuildVersion.MajorVersion
	if xcodeMajorVersion < minSupportedXcodeMajorVersion {
		return ExportOptions{}, fmt.Errorf("invalid xcode major version (%d), should not be less then min supported: %d", xcodeMajorVersion, minSupportedXcodeMajorVersion)
	}

	// ABS out dir pth
	absOutputDir, err := pathutil.AbsPath(cfg.OutputDir)
	if err != nil {
		return ExportOptions{}, fmt.Errorf("failed to expand OutputDir (%s), error: %s", cfg.OutputDir, err)
	}

	if exist, err := pathutil.IsPathExists(absOutputDir); err != nil {
		return ExportOptions{}, fmt.Errorf("failed to check if OutputDir exist, error: %s", err)
	} else if !exist {
		if err := os.MkdirAll(absOutputDir, 0777); err != nil {
			return ExportOptions{}, fmt.Errorf("failed to create OutputDir (%s), error: %s", absOutputDir, err)
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
				return ExportOptions{}, fmt.Errorf("failed to remove path (%s), error: %s", pth, err)
			}

		}
	}

	absProjectPath, err := filepath.Abs(cfg.ProjectPath)
	if err != nil {
		return ExportOptions{}, fmt.Errorf("failed to get absolute project path: %s", err)
	}

	//
	// Read the Scheme
	var scheme *xcscheme.Scheme
	var schemeContainerDir string
	var conf string
	{
		scheme, schemeContainerDir, err = readScheme(absProjectPath, cfg.Scheme)

		if err != nil {
			return ExportOptions{}, fmt.Errorf("failed to read schema: %s", err)
		}

		if cfg.Configuration != "" {
			conf = cfg.Configuration
		} else {
			conf = scheme.ArchiveAction.BuildConfiguration
		}
	}

	//
	// Create the app with Xcode Command Line tools
	{
		fmt.Println()
		log.Infof("Running build")

		var isWorkspace bool
		if xcworkspace.IsWorkspace(absProjectPath) {
			isWorkspace = true
		} else if !xcodeproj.IsXcodeProj(absProjectPath) {
			return ExportOptions{}, fmt.Errorf("project file extension should be .xcodeproj or .xcworkspace, but got: %s", filepath.Ext(absProjectPath))
		}

		// Build for simulator command
		xcodeBuildCmd := xcodebuild.NewCommandBuilder(absProjectPath, isWorkspace, xcodebuild.BuildAction)
		xcodeBuildCmd.SetScheme(cfg.Scheme)
		xcodeBuildCmd.SetConfiguration(conf)

		// Set simulator destination and disable code signing for the build
		xcodeBuildCmd.SetDestination("generic/platform=iOS Simulator")

		var customBuildActions []string

		// Clean build
		if cfg.IsCleanBuild {
			customBuildActions = append(customBuildActions, "clean")
		}

		// Disable indexing while building
		if cfg.DisableIndexWhileBuilding {
			customBuildActions = append(customBuildActions, "COMPILER_INDEX_STORE_ENABLE=NO")
		}

		// Explicitly specify if code signing is allowed
		if cfg.CodeSigningAllowed {
			customBuildActions = append(customBuildActions, "CODE_SIGNING_ALLOWED=YES")
		} else {
			customBuildActions = append(customBuildActions, "CODE_SIGNING_ALLOWED=NO")
		}

		xcodeBuildCmd.SetCustomBuildAction(customBuildActions...)

		// XcodeBuild Options
		if cfg.XcodebuildOptions != "" {
			options, err := shellquote.Split(cfg.XcodebuildOptions)
			if err != nil {
				return ExportOptions{}, fmt.Errorf("failed to shell split XcodebuildOptions (%s), error: %s", cfg.XcodebuildOptions, err)
			}
			xcodeBuildCmd.SetCustomOptions(options)
		}

		var swiftPackagesPath string
		if xcodeMajorVersion >= 11 {
			var err error
			if swiftPackagesPath, err = cache.SwiftPackagesPath(absProjectPath); err != nil {
				return ExportOptions{}, fmt.Errorf("failed to get Swift Packages path, error: %s", err)
			}
		}

		rawXcodeBuildOut, err := runCommandWithRetry(xcodeBuildCmd, cfg.OutputTool == "xcpretty", swiftPackagesPath)
		if err != nil {
			if cfg.OutputTool == "xcpretty" {
				log.Errorf("\nLast lines of the Xcode's build log:")
				fmt.Println(stringutil.LastNLines(rawXcodeBuildOut, 10))

				if err := utils.ExportOutputFileContent(rawXcodeBuildOut, rawXcodebuildOutputLogPath, bitriseXcodeRawResultTextEnvKey); err != nil {
					log.Warnf("Failed to export %s, error: %s", bitriseXcodeRawResultTextEnvKey, err)
				} else {
					log.Warnf(`You can find the last couple of lines of Xcode's build log above, but the full log is also available in the raw-xcodebuild-output.log
The log file is stored in $BITRISE_DEPLOY_DIR, and its full path is available in the $BITRISE_XCODE_RAW_RESULT_TEXT_PATH environment variable
(value: %s)`, rawXcodebuildOutputLogPath)
				}
			}
			return ExportOptions{}, fmt.Errorf("build failed, error: %s", err)
		}
	}

	//
	// Export artifacts
	var exportedArtifacts []string
	{
		fmt.Println()
		log.Infof("Copy artifacts from Derived Data to %s", absOutputDir)

		proj, err := findBuiltProject(scheme, schemeContainerDir, conf)
		if err != nil {
			return ExportOptions{}, fmt.Errorf("failed to open xcproj - (%s), error: %s", absProjectPath, err)
		}

		customOptions, err := shellquote.Split(cfg.XcodebuildOptions)
		if err != nil {
			return ExportOptions{}, fmt.Errorf("failed to shell split XcodebuildOptions (%s), error: %s", cfg.XcodebuildOptions, err)
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

		schemeBuildDir, err := buildTargetDirForScheme(proj, absProjectPath, *scheme, conf, customOptions...)
		if err != nil {
			return ExportOptions{}, fmt.Errorf("failed to get scheme (%s) build target dir, error: %s", cfg.Scheme, err)
		}

		log.Debugf("Scheme build dir: %s", schemeBuildDir)

		// Export the artifact from the build dir to the output_dir
		if exportedArtifacts, err = exportArtifacts(proj, *scheme, schemeBuildDir, conf, cfg.SimulatorPlatform, absOutputDir); err != nil {
			return ExportOptions{}, fmt.Errorf("failed to export the artifacts, error: %s", err)
		}
	}

	exportOptions := ExportOptions{
		Artifacts: exportedArtifacts,
		OutputDir: absOutputDir,
	}

	return exportOptions, nil
}

// ExportOptions ...
type ExportOptions struct {
	Artifacts []string
	OutputDir string
}

// ExportOutput ...
func (b BuildForSimulatorStep) ExportOutput(options ExportOptions) error {
	fmt.Println()
	log.Infof("Exporting outputs")
	if len(options.Artifacts) == 0 {
		log.Warnf("No exportable artifact have found.")
	} else {
		mainTargetAppPath, pathMap, err := exportOutput(options.Artifacts)
		if err != nil {
			return fmt.Errorf("failed to export outputs (BITRISE_APP_DIR_PATH & BITRISE_APP_DIR_PATH_LIST), error: %s", err)
		}

		log.Donef("BITRISE_APP_DIR_PATH -> %s", mainTargetAppPath)
		log.Donef("BITRISE_APP_DIR_PATH_LIST -> %s", pathMap)

		fmt.Println()
		log.Donef("You can find the exported artifacts in: %s", options.OutputDir)
	}
	return nil
}

// Ancillary Methods

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

func readScheme(pth, schemeName string) (*xcscheme.Scheme, string, error) {
	var scheme *xcscheme.Scheme
	var schemeContainerDir string

	if xcodeproj.IsXcodeProj(pth) {
		project, err := xcodeproj.Open(pth)
		if err != nil {
			return nil, "", err
		}

		scheme, _, err = project.Scheme(schemeName)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get scheme (%s) from project (%s), error: %s", schemeName, pth, err)
		}
		schemeContainerDir = filepath.Dir(pth)
	} else if xcworkspace.IsWorkspace(pth) {
		workspace, err := xcworkspace.Open(pth)
		if err != nil {
			return nil, "", err
		}

		var containerProject string
		scheme, containerProject, err = workspace.Scheme(schemeName)

		if err != nil {
			return nil, "", fmt.Errorf("no scheme found with name: %s in workspace: %s, error: %s", schemeName, pth, err)
		}
		schemeContainerDir = filepath.Dir(containerProject)
	} else {
		return nil, "", fmt.Errorf("unknown project extension: %s", filepath.Ext(pth))
	}
	return scheme, schemeContainerDir, nil
}

// findBuiltProject returns the Xcode project which will be built for the provided scheme
func findBuiltProject(scheme *xcscheme.Scheme, schemeContainerDir, configurationName string) (xcodeproj.XcodeProj, error) {
	if configurationName == "" {
		configurationName = scheme.ArchiveAction.BuildConfiguration
	}

	if configurationName == "" {
		return xcodeproj.XcodeProj{}, fmt.Errorf("no configuration provided nor default defined for the scheme's (%s) archive action", scheme.Name)
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
		return xcodeproj.XcodeProj{}, fmt.Errorf("archivable entry not found")
	}

	projectPth, err := archiveEntry.BuildableReference.ReferencedContainerAbsPath(schemeContainerDir)
	if err != nil {
		return xcodeproj.XcodeProj{}, err
	}

	project, err := xcodeproj.Open(projectPth)
	if err != nil {
		return xcodeproj.XcodeProj{}, err
	}

	return project, nil
}

// buildTargetDirForScheme returns the TARGET_BUILD_DIR for the provided scheme
func buildTargetDirForScheme(proj xcodeproj.XcodeProj, projectPath string, scheme xcscheme.Scheme, configuration string, customOptions ...string) (string, error) {
	// Fetch project's main target from .xcodeproject
	var buildSettings serialized.Object
	if xcodeproj.IsXcodeProj(projectPath) {
		mainTarget, err := mainTargetOfScheme(scheme, proj.Proj.Targets)
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

		buildSettings, err = workspace.SchemeBuildSettings(scheme.Name, configuration, customOptions...)
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

func wrapperNameForScheme(proj xcodeproj.XcodeProj, projectPath string, scheme xcscheme.Scheme, configuration string, customOptions ...string) (string, error) {
	// Fetch project's main target from .xcodeproject
	var buildSettings serialized.Object
	if xcodeproj.IsXcodeProj(projectPath) {
		mainTarget, err := mainTargetOfScheme(scheme, proj.Proj.Targets)
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
			return "", fmt.Errorf("failed to open xcworkspace (%s), error: %s", projectPath, err)
		}

		buildSettings, err = workspace.SchemeBuildSettings(scheme.Name, configuration, customOptions...)
		if err != nil {
			return "", fmt.Errorf("failed to parse workspace (%s) build settings, error: %s", projectPath, err)
		}
	} else {
		return "", fmt.Errorf("project file extension should be .xcodeproj or .xcworkspace, but got: %s", filepath.Ext(projectPath))

	}

	wrapperName, err := buildSettings.String("WRAPPER_NAME")

	if err != nil {
		return "", fmt.Errorf("failed to parse build settings, error: %s", err)
	}

	return wrapperName, nil
}

// exportArtifacts exports the main target and it's .app dependencies.
func exportArtifacts(proj xcodeproj.XcodeProj, scheme xcscheme.Scheme, schemeBuildDir, configuration, simulatorPlatform, deployDir string, customOptions ...string) ([]string, error) {
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

	mainTarget, err := mainTargetOfScheme(scheme, proj.Proj.Targets)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch project's targets, error: %s", err)
	}

	targets := append([]xcodeproj.Target{mainTarget}, mainTarget.DependentExecutableProductTargets(false)...)

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
		options := []string{"-sdk", simulatorName}
		var targetDir string
		{
			if sliceutil.IsStringInSlice("-sdk", customOptions) {
				options = customOptions
			} else {
				options = append(options, customOptions...)
			}

			buildSettings, err := proj.TargetBuildSettings(target.Name, configuration, options...)
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
					wrapperName, err := wrapperNameForScheme(proj, proj.Path, scheme, configuration, customOptions...)
					if err != nil {
						return nil, fmt.Errorf("failed to get scheme (%s) build target dir, error: %s", scheme, err)
					}
					source = filepath.Join(sourceDir, wrapperName)

					if exists, err := pathutil.IsPathExists(source); err != nil {
						log.Debugf("failed to check if the path exists: (%s), error: ", source, err)
						continue
					} else if !exists {
						log.Debugf("2nd path does not exist: %s", source)
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
func mainTargetOfScheme(scheme xcscheme.Scheme, targets []xcodeproj.Target) (xcodeproj.Target, error) {
	var blueIdent string
	for _, entry := range scheme.BuildAction.BuildActionEntries {
		if entry.BuildableReference.IsAppReference() {
			blueIdent = entry.BuildableReference.BlueprintIdentifier
			break
		}
	}

	// Search for the main target
	for _, t := range targets {
		if t.ID == blueIdent {
			return t, nil
		}
	}
	return xcodeproj.Target{}, fmt.Errorf("failed to find the project's main target for scheme (%s)", scheme.Name)
}
