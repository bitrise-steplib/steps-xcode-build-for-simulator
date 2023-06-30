package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bitrise-io/go-steputils/output"
	"github.com/bitrise-io/go-steputils/stepconf"
	"github.com/bitrise-io/go-steputils/tools"
	"github.com/bitrise-io/go-utils/errorutil"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-io/go-utils/sliceutil"
	"github.com/bitrise-io/go-utils/stringutil"
	"github.com/bitrise-io/go-utils/v2/fileutil"
	v2pathutil "github.com/bitrise-io/go-utils/v2/pathutil"
	"github.com/bitrise-io/go-xcode/v2/destination"
	"github.com/bitrise-io/go-xcode/v2/xcconfig"
	"github.com/bitrise-io/go-xcode/xcodebuild"
	cache "github.com/bitrise-io/go-xcode/xcodecache"
	"github.com/bitrise-io/go-xcode/xcodeproject/serialized"
	"github.com/bitrise-io/go-xcode/xcodeproject/xcodeproj"
	"github.com/bitrise-io/go-xcode/xcodeproject/xcscheme"
	"github.com/bitrise-io/go-xcode/xcodeproject/xcworkspace"
	"github.com/bitrise-io/go-xcode/xcpretty"
	"github.com/bitrise-steplib/steps-xcode-build-for-simulator/util"
	"github.com/kballard/go-shellquote"
)

type simulatorSDK string

const (
	iOSSimSDK     simulatorSDK = "iphonesimulator"
	tvOSSimSDK    simulatorSDK = "appletvsimulator"
	watchOSSimSDK simulatorSDK = "watchsimulator"
)

const (
	xcodebuilgLogFileName      = "xcodebuild_build.log"
	bitriseXcodebuildLogEnvKey = "BITRISE_XCODEBUILD_BUILD_FOR_SIMULATOR_LOG_PATH"
)

type Config struct {
	ProjectPath string `env:"project_path,required"`
	Scheme      string `env:"scheme,required"`
	Destination string `env:"destination,required"`

	// xcodebuild configuration
	Configuration               string `env:"configuration"`
	XCConfigContent             string `env:"xcconfig_content"`
	PerformCleanAction          bool   `env:"perform_clean_action,opt[yes,no]"`
	XcodebuildAdditionalOptions string `env:"xcodebuild_options"`
	LogFormatter                string `env:"log_formatter,opt[xcpretty,xcodebuild]"`

	// Output export
	OutputDir string `env:"output_dir,required"`

	// Caching
	CacheLevel string `env:"cache_level,opt[none,swift_packages]"`

	// Debugging
	VerboseLog bool `env:"verbose_log,required"`
}

type RunOpts struct {
	ProjectPath  string
	Scheme       string
	Destination  string
	SimulatorSDK simulatorSDK

	Configuration               string
	XCConfigContent             string
	PerformCleanAction          bool
	XcodebuildAdditionalOptions []string
	LogFormatter                string

	OutputDir string

	CacheLevel string
}

// BuildForSimulatorStep ...
type BuildForSimulatorStep struct {
	pathProvider   v2pathutil.PathProvider
	pathChecker    v2pathutil.PathChecker
	pathModifier   v2pathutil.PathModifier
	fileManager    fileutil.FileManager
	XCConfigWriter xcconfig.Writer
}

// NewBuildForSimulatorStep ...
func NewBuildForSimulatorStep(pathProvider v2pathutil.PathProvider, pathChecker v2pathutil.PathChecker, pathModifier v2pathutil.PathModifier, fileManager fileutil.FileManager) BuildForSimulatorStep {
	xcconfigWriter := xcconfig.NewWriter(pathProvider, fileManager, pathChecker, pathModifier)
	return BuildForSimulatorStep{
		pathProvider:   pathProvider,
		pathChecker:    pathChecker,
		pathModifier:   pathModifier,
		fileManager:    fileManager,
		XCConfigWriter: xcconfigWriter,
	}
}

// ProcessConfig ...
func (b BuildForSimulatorStep) ProcessConfig() (RunOpts, error) {
	var config Config
	if err := stepconf.Parse(&config); err != nil {
		return RunOpts{}, fmt.Errorf("unable to parse input: %s", err)
	}

	log.SetEnableDebugLog(config.VerboseLog)
	stepconf.Print(config)

	destinationSpecifier, err := destination.NewSpecifier(config.Destination)
	if err != nil {
		return RunOpts{}, fmt.Errorf("invalid input `destination` (%s): %w", config.Destination, err)
	}

	platform, isGeneric := destinationSpecifier.Platform()
	if !isGeneric {
		log.Warnf("input `destination` (%s) is not a generic destination, key 'generic/platform' preferred", config.Destination)
	}

	var simulatorSDK simulatorSDK
	switch platform {
	case destination.IOSSimulator:
		simulatorSDK = iOSSimSDK
	case destination.TvOSSimulator:
		simulatorSDK = tvOSSimSDK
	case destination.WatchOSSimulator:
		simulatorSDK = watchOSSimSDK
	default:
		return RunOpts{}, fmt.Errorf("unsupported destination (%s); iOS, tvOS or watchOS Simulator expected", platform)
	}

	additionalOptions, err := shellquote.Split(config.XcodebuildAdditionalOptions)
	if err != nil {
		return RunOpts{}, fmt.Errorf("provided `xcodebuild_options` (%s) are not valid CLI parameters: %s", config.XcodebuildAdditionalOptions, err)
	}

	if strings.TrimSpace(config.XCConfigContent) == "" {
		config.XCConfigContent = ""
	}
	if sliceutil.IsStringInSlice("-xcconfig", additionalOptions) &&
		config.XCConfigContent != "" {
		return RunOpts{}, fmt.Errorf("`-xcconfig` option found in `xcodebuild_options`, please clear `xcconfig_content` input as can not set both")
	}

	return RunOpts{
		ProjectPath:  config.ProjectPath,
		Scheme:       config.Scheme,
		Destination:  config.Destination,
		SimulatorSDK: simulatorSDK,

		Configuration:               config.Configuration,
		XCConfigContent:             config.XCConfigContent,
		PerformCleanAction:          config.PerformCleanAction,
		XcodebuildAdditionalOptions: additionalOptions,
		LogFormatter:                config.LogFormatter,

		OutputDir: config.OutputDir,

		CacheLevel: config.CacheLevel,
	}, nil
}

// InstallDependencies ...
func (b BuildForSimulatorStep) InstallDependencies(cfg RunOpts) (RunOpts, error) {
	if cfg.LogFormatter != "xcpretty" {
		return cfg, nil
	}

	outputTool := cfg.LogFormatter

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

					cfg.LogFormatter = "xcodebuild"
					return cfg, nil
				}
			}
		}
	}
	xcprettyVersion, err := xcpretty.Version()
	if err != nil {
		log.Warnf("Failed to determine xcpretty version, error: %s", err)
		log.Printf("Switching to xcodebuild for output tool")

		cfg.LogFormatter = "xcodebuild"
		return cfg, nil
	}

	log.Printf("- xcprettyVersion: %s", xcprettyVersion.String())
	cfg.LogFormatter = outputTool

	return cfg, nil
}

// Run ...
func (s BuildForSimulatorStep) Run(cfg RunOpts) (ExportOptions, error) {
	// ABS out dir pth
	absOutputDir, err := s.pathModifier.AbsPath(cfg.OutputDir)
	if err != nil {
		return ExportOptions{}, fmt.Errorf("failed to expand `output_dir` (%s): %s", cfg.OutputDir, err)
	}

	if exist, err := s.pathChecker.IsPathExists(absOutputDir); err != nil {
		return ExportOptions{}, fmt.Errorf("failed to check if `output_dir` exist: %s", err)
	} else if !exist {
		if err := os.MkdirAll(absOutputDir, 0777); err != nil {
			return ExportOptions{}, fmt.Errorf("failed to create `output_dir` (%s): %s", absOutputDir, err)
		}
	}

	// Output files
	rawXcodebuildOutputLogPath := filepath.Join(absOutputDir, xcodebuilgLogFileName)

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

		actions := []string{"build"}
		if cfg.PerformCleanAction {
			actions = append(actions, "clean")
		}

		// Build for simulator command
		xcodeBuildCmd := xcodebuild.NewCommandBuilder(absProjectPath, actions...)
		xcodeBuildCmd.SetScheme(cfg.Scheme)
		xcodeBuildCmd.SetConfiguration(conf)
		xcodeBuildCmd.SetDestination(cfg.Destination)
		xcodeBuildCmd.SetCustomOptions(cfg.XcodebuildAdditionalOptions)
		if cfg.XCConfigContent != "" {
			xcconfigPath, err := s.XCConfigWriter.Write(cfg.XCConfigContent)
			if err != nil {
				return ExportOptions{}, fmt.Errorf("failed to write xcconfig file contents: %w", err)
			}
			xcodeBuildCmd.SetXCConfigPath(xcconfigPath)
		}

		swiftPackagesPath, err := cache.SwiftPackagesPath(absProjectPath)
		if err != nil {
			return ExportOptions{}, fmt.Errorf("failed to get Swift Packages path: %s", err)
		}

		rawXcodeBuildOut, err := runCommandWithRetry(xcodeBuildCmd, cfg.LogFormatter == "xcpretty", swiftPackagesPath)
		if err != nil {
			if cfg.LogFormatter == "xcpretty" {
				log.Errorf("\nLast lines of the Xcode's build log:")
				fmt.Println(stringutil.LastNLines(rawXcodeBuildOut, 10))

				if err := output.ExportOutputFileContent(rawXcodeBuildOut, rawXcodebuildOutputLogPath, bitriseXcodebuildLogEnvKey); err != nil {
					log.Warnf("Failed to export %s, error: %s", bitriseXcodebuildLogEnvKey, err)
				} else {
					log.Warnf(`You can find the last couple of lines of Xcode's build log above, but the full log is also available in the %s.ß
The log file is stored in $BITRISE_DEPLOY_DIR, and its full path is available in the %s environment variable
(value: %s)`, xcodebuilgLogFileName, bitriseXcodebuildLogEnvKey, rawXcodebuildOutputLogPath)
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

		customOptions := cfg.XcodebuildAdditionalOptions
		customOptions = append(customOptions, "-sdk", string(cfg.SimulatorSDK))

		schemeBuildDir, err := buildTargetDirForScheme(proj, absProjectPath, *scheme, conf, customOptions...)
		if err != nil {
			return ExportOptions{}, fmt.Errorf("failed to get scheme (%s) build target dir, error: %s", cfg.Scheme, err)
		}

		log.Debugf("Scheme build dir: %s", schemeBuildDir)

		// Export the artifact from the build dir to the output_dir
		if exportedArtifacts, err = exportArtifacts(proj, *scheme, schemeBuildDir, conf, cfg.SimulatorSDK, absOutputDir); err != nil {
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
func exportArtifacts(proj xcodeproj.XcodeProj, scheme xcscheme.Scheme, schemeBuildDir, configuration string, simulatorSDK simulatorSDK, deployDir string, customOptions ...string) ([]string, error) {
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

	targets := append([]xcodeproj.Target{mainTarget}, proj.DependentTargetsOfTarget(mainTarget)...)
	for _, target := range targets {
		log.Donef(target.Name + "...")

		// Is the target an application? -> If not skip the export
		if !target.IsAppProduct() {
			log.Printf("Target (%s) is not an .app - SKIP", target.Name)
			continue
		}

		//
		// Find out the sdk for the target
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
				simulatorSDK = watchOSSimSDK
			}
		}

		//
		// Find the TARGET_BUILD_DIR for the target
		options := []string{"-sdk", string(simulatorSDK)}
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
						return nil, fmt.Errorf("failed to get Scheme (%s) build target dir, error: %s", scheme.Name, err)
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
