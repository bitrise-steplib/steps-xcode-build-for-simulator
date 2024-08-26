package main

import (
	"fmt"
	"io/fs"
	"os"
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
	"github.com/bitrise-io/go-xcode/xcpretty"
	"github.com/bitrise-steplib/steps-xcode-build-for-simulator/util"
	"github.com/kballard/go-shellquote"
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

	tmpDir, err := pathutil.NormalizedOSTempDirPath("xcodeArchive")
	if err != nil {
		return ExportOptions{}, fmt.Errorf("failed to create temp dir, error: %s", err)
	}
	archivePth := filepath.Join(tmpDir, cfg.Scheme+"-simulator.xcarchive")
	{
		fmt.Println()
		log.Infof("Running build")

		actions := []string{"archive"}
		if cfg.PerformCleanAction {
			actions = append(actions, "clean")
		}

		archiveCmd := xcodebuild.NewCommandBuilder(absProjectPath, actions...)
		archiveCmd.SetArchivePath(archivePth)
		archiveCmd.SetScheme(cfg.Scheme)
		if cfg.Configuration != "" {
			archiveCmd.SetConfiguration(cfg.Configuration)
		}
		archiveCmd.SetDestination(cfg.Destination)
		archiveCmd.SetCustomOptions(cfg.XcodebuildAdditionalOptions)
		if cfg.XCConfigContent != "" {
			xcconfigPath, err := s.XCConfigWriter.Write(cfg.XCConfigContent)
			if err != nil {
				return ExportOptions{}, fmt.Errorf("failed to write xcconfig file contents: %w", err)
			}
			archiveCmd.SetXCConfigPath(xcconfigPath)
		}

		swiftPackagesPath, err := cache.SwiftPackagesPath(absProjectPath)
		if err != nil {
			return ExportOptions{}, fmt.Errorf("failed to get Swift Packages path: %s", err)
		}

		rawXcodeBuildOut, err := runCommandWithRetry(archiveCmd, cfg.LogFormatter == "xcpretty", swiftPackagesPath)
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

	// Export artifacts
	fmt.Println()
	log.Infof("Copy artifacts to $BITRISE_DEPLOY_DIR")

	exportedArtifacts, err := exportArtifacts(archivePth, absOutputDir)
	if err != nil {
		return ExportOptions{}, fmt.Errorf("failed to export the artifacts, error: %s", err)
	}

	return ExportOptions{
		Artifacts: exportedArtifacts,
		OutputDir: absOutputDir,
	}, nil
}

type ExportOptions struct {
	Artifacts []string
	OutputDir string
}

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

// exportArtifacts exports the main target and it's .app dependencies.
func exportArtifacts(archivePath string, deployDir string) ([]string, error) {
	var exportedArtifacts []string

	if err := filepath.WalkDir(archivePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() && filepath.Ext(d.Name()) == ".app" {
			destination := filepath.Join(deployDir, d.Name())

			cmd := util.CopyDir(path, destination)
			cmd.SetStdout(os.Stdout)
			cmd.SetStderr(os.Stderr)
			log.Debugf("$ " + cmd.PrintableCommandArgs())
			if err := cmd.Run(); err != nil {
				log.Debugf("failed to copy the generated app from (%s) to the Deploy dir", path)
				return err
			}

			exportedArtifacts = append(exportedArtifacts, destination)
		}

		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk through the archive path: %s", err)
	}

	if len(exportedArtifacts) != 0 {
		log.Debugf("Success\n")
	} else {
		return nil, fmt.Errorf("failed to copy the generated app to the Deploy dir")
	}

	return exportedArtifacts, nil
}
