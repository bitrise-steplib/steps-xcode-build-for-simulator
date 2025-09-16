package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bitrise-io/go-steputils/v2/ruby"
	"github.com/bitrise-io/go-steputils/v2/stepconf"
	"github.com/bitrise-io/go-utils/sliceutil"
	"github.com/bitrise-io/go-utils/v2/command"
	"github.com/bitrise-io/go-utils/v2/env"
	"github.com/bitrise-io/go-utils/v2/errorutil"
	"github.com/bitrise-io/go-utils/v2/fileutil"
	"github.com/bitrise-io/go-utils/v2/log"
	"github.com/bitrise-io/go-utils/v2/pathutil"
	"github.com/bitrise-io/go-xcode/v2/xcodecommand"
	"github.com/bitrise-io/go-xcode/v2/xcodeversion"
	"github.com/kballard/go-shellquote"

	archive "github.com/bitrise-steplib/steps-xcode-archive/step"
)

const (
	minSupportedXcodeMajorVersion = 9
	bitriseAppDirPathListKey      = "BITRISE_APP_DIR_PATH_LIST"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := log.NewLogger()
	configParser := NewConfigParser(logger)
	config, err := configParser.ProcessInputs()
	if err != nil {
		logger.Errorf("%s", errorutil.FormattedError(fmt.Errorf("failed to process Step inputs: %w", err)))
		return 1
	}

	archiver, err := createXcodebuildArchiver(logger, config.LogFormatter)
	if err != nil {
		logger.Errorf("%s", errorutil.FormattedError(fmt.Errorf("Failed to process Step inputs: %w", err)))
		return 1
	}

	archiver.EnsureDependencies()

	exitCode := 0
	runOpts := createRunOptions(config)
	result, err := archiver.Run(runOpts)
	if err != nil {
		logger.Errorf("%s", errorutil.FormattedError(fmt.Errorf("Failed to execute Step main logic: %w", err)))
		exitCode = 1
		// don't return as step outputs needs to be exported even in case of failure (for example the xcodebuild logs)
	}

	exportOpts := createExportOptions(config, result)
	if err := archiver.ExportOutput(exportOpts); err != nil {
		logger.Errorf("%s", errorutil.FormattedError(fmt.Errorf("Failed to export Step outputs: %w", err)))
		exitCode = 1
	}

	deployAllApps(config.OutputDir, result, logger)

	return exitCode
}

type Inputs struct {
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

	// Debugging
	VerboseLog bool `env:"verbose_log,required"`
}

// Config ...
type Config struct {
	Inputs
	XcodeMajorVersion           int
	XcodebuildAdditionalOptions []string
}

type ConfigParser struct {
	stepInputParser    stepconf.InputParser
	pathProvider       pathutil.PathProvider
	pathChecker        pathutil.PathChecker
	xcodeVersionReader xcodeversion.Reader
	fileManager        fileutil.FileManager
	cmdFactory         command.Factory
	logger             log.Logger
}

func NewConfigParser(logger log.Logger) ConfigParser {
	envRepository := env.NewRepository()
	inputParser := stepconf.NewInputParser(envRepository)
	pathProvider := pathutil.NewPathProvider()
	pathChecker := pathutil.NewPathChecker()
	fileManager := fileutil.NewFileManager()
	cmdFactory := command.NewFactory(envRepository)
	xcodeVersionReader := xcodeversion.NewXcodeVersionProvider(cmdFactory)

	return ConfigParser{
		stepInputParser:    inputParser,
		pathProvider:       pathProvider,
		pathChecker:        pathChecker,
		xcodeVersionReader: xcodeVersionReader,
		fileManager:        fileManager,
		cmdFactory:         cmdFactory,
		logger:             logger,
	}
}

// ProcessInputs ...
func (p ConfigParser) ProcessInputs() (Config, error) {
	var inputs Inputs
	if err := p.stepInputParser.Parse(&inputs); err != nil {
		return Config{}, fmt.Errorf("issue with input: %s", err)
	}

	stepconf.Print(inputs)
	p.logger.Println()

	config := Config{Inputs: inputs}
	p.logger.EnableDebugLog(config.VerboseLog)

	var err error
	config.XcodebuildAdditionalOptions, err = shellquote.Split(inputs.XcodebuildAdditionalOptions)
	if err != nil {
		return Config{}, fmt.Errorf("provided XcodebuildAdditionalOptions (%s) are not valid CLI parameters: %s", inputs.XcodebuildAdditionalOptions, err)
	}

	if strings.TrimSpace(config.XCConfigContent) == "" {
		config.XCConfigContent = ""
	}
	if sliceutil.IsStringInSlice("-xcconfig", config.XcodebuildAdditionalOptions) &&
		config.XCConfigContent != "" {
		return Config{}, fmt.Errorf("`-xcconfig` option found in XcodebuildOptions (`xcodebuild_options`), please clear Build settings (xcconfig) (`xcconfig_content`) input as only one can be set")
	}

	if filepath.Ext(config.ProjectPath) != ".xcodeproj" && filepath.Ext(config.ProjectPath) != ".xcworkspace" {
		return Config{}, fmt.Errorf("issue with input ProjectPath: should be and .xcodeproj or .xcworkspace path")
	}

	p.logger.Infof("Xcode version:")

	// Detect Xcode major version
	xcodebuildVersion, err := p.xcodeVersionReader.GetVersion()
	if err != nil {
		return Config{}, fmt.Errorf("failed to determine xcode version, error: %s", err)
	}
	p.logger.Printf("%s (%s)", xcodebuildVersion.Version, xcodebuildVersion.BuildVersion)

	if xcodebuildVersion.Major < minSupportedXcodeMajorVersion {
		return Config{}, fmt.Errorf("invalid xcode major version (%d), should not be less then min supported: %d", xcodebuildVersion.Major, minSupportedXcodeMajorVersion)
	}
	config.XcodeMajorVersion = int(xcodebuildVersion.Major)

	absProjectPath, err := filepath.Abs(config.ProjectPath)
	if err != nil {
		return Config{}, fmt.Errorf("failed to get absolute project path, error: %s", err)
	}
	config.ProjectPath = absProjectPath

	// abs out dir pth
	absOutputDir, err := filepath.Abs(config.OutputDir)
	if err != nil {
		return Config{}, fmt.Errorf("failed to expand OutputDir (%s), error: %s", config.OutputDir, err)
	}
	config.OutputDir = absOutputDir

	if exist, err := p.pathChecker.IsPathExists(config.OutputDir); err != nil {
		return Config{}, fmt.Errorf("failed to check if OutputDir exist, error: %s", err)
	} else if !exist {
		if err := os.MkdirAll(config.OutputDir, 0777); err != nil {
			return Config{}, fmt.Errorf("failed to create OutputDir (%s), error: %s", config.OutputDir, err)
		}
	}

	return config, nil
}

func createXcodebuildArchiver(logger log.Logger, logFormatter string) (archive.XcodebuildArchiver, error) {
	envRepository := env.NewRepository()
	pathProvider := pathutil.NewPathProvider()
	pathChecker := pathutil.NewPathChecker()
	pathModifier := pathutil.NewPathModifier()
	fileManager := fileutil.NewFileManager()
	cmdFactory := command.NewFactory(envRepository)
	xcodeVersionReader := xcodeversion.NewXcodeVersionProvider(cmdFactory)

	xcodeCommandRunner := xcodecommand.Runner(nil)
	switch logFormatter {
	case archive.XcodebuildTool:
		xcodeCommandRunner = xcodecommand.NewRawCommandRunner(logger, cmdFactory)
	case archive.XcbeautifyTool:
		xcodeCommandRunner = xcodecommand.NewXcbeautifyRunner(logger, cmdFactory)
	case archive.XcprettyTool:
		commandLocator := env.NewCommandLocator()
		rubyComamndFactory, err := ruby.NewCommandFactory(cmdFactory, commandLocator)
		if err != nil {
			return archive.XcodebuildArchiver{}, fmt.Errorf("failed to install xcpretty: %s", err)
		}
		rubyEnv := ruby.NewEnvironment(rubyComamndFactory, commandLocator, logger)

		xcodeCommandRunner = xcodecommand.NewXcprettyCommandRunner(logger, cmdFactory, pathChecker, fileManager, rubyComamndFactory, rubyEnv)
	default:
		panic(fmt.Sprintf("Unknown log formatter: %s", logFormatter))
	}

	return archive.NewXcodebuildArchiver(xcodeCommandRunner, logFormatter, xcodeVersionReader, pathProvider, pathChecker, pathModifier, fileManager, cmdFactory, logger), nil
}

func createRunOptions(config Config) archive.RunOpts {
	platform, err := archive.ParsePlatform(config.Destination)
	if err != nil {
		platform = archive.Platform("iOS Simulator") // default to iOS Simulator if parsing fails
	}

	return archive.RunOpts{
		ProjectPath:         config.ProjectPath,
		Scheme:              config.Scheme,
		DestinationPlatform: platform,
		Configuration:       config.Configuration,
		XcodeMajorVersion:   config.XcodeMajorVersion,
		ArtifactName:        config.Scheme,

		CodesignManager: nil,

		PerformCleanAction:          config.PerformCleanAction,
		XcconfigContent:             config.XCConfigContent,
		XcodebuildAdditionalOptions: config.XcodebuildAdditionalOptions,
		CacheLevel:                  "none", // This step haven't done this before, so keeping it none for now

		CustomExportOptionsPlistContent: "",
		ExportMethod:                    "development",
		TestFlightInternalTestingOnly:   false,
		ICloudContainerEnvironment:      "",
		ExportDevelopmentTeam:           "",
		UploadBitcode:                   false,
		CompileBitcode:                  false,
	}
}

func createExportOptions(config Config, result archive.RunResult) archive.ExportOpts {
	mainAppPathBase := filepath.Base(result.Archive.Application.Path)
	mainAppPathExt := filepath.Ext(result.Archive.Application.Path)

	return archive.ExportOpts{
		OutputDir:      config.OutputDir,
		ArtifactName:   strings.TrimSuffix(mainAppPathBase, mainAppPathExt),
		ExportAllDsyms: true,

		Archive: result.Archive,

		ExportOptionsPath: result.ExportOptionsPath,
		IPAExportDir:      result.IPAExportDir,

		XcodebuildArchiveLog:       result.XcodebuildArchiveLog,
		XcodebuildExportArchiveLog: result.XcodebuildExportArchiveLog,
		IDEDistrubutionLogsDir:     result.IDEDistrubutionLogsDir,
	}
}

func deployAllApps(outputDir string, result archive.RunResult, logger log.Logger) {
	envRepository := env.NewRepository()
	cmdFactory := command.NewFactory(envRepository)
	pathChecker := pathutil.NewPathChecker()

	paths := getAppPathsFromResult(result)
	var deployPaths []string
	for _, p := range paths {
		if !strings.HasSuffix(filepath.Base(p), "app") {
			continue
		}
		dst := filepath.Join(outputDir, filepath.Base(p))
		exists, err := pathChecker.IsDirExists(dst)
		if err == nil && !exists {
			if err := copy(cmdFactory, p, dst); err != nil {
				logger.Errorf("failed to copy %s to deploy folder", p)
			}
		}

		deployPaths = append(deployPaths, dst)
	}
	err := exportEnvironmentWithEnvman(cmdFactory, bitriseAppDirPathListKey, strings.Join(deployPaths, "|"))
	if err != nil {
		logger.Errorf("failed to export variable %s", bitriseAppDirPathListKey)
	}
}

func getAppPathsFromResult(result archive.RunResult) []string {
	var paths []string

	if result.Archive == nil {
		return paths
	}

	// Base
	paths = append(paths, result.Archive.Application.Path)

	// Clip
	if clipApp := result.Archive.Application.ClipApplication; clipApp != nil {
		if clipAppPath := clipApp.Path; clipAppPath != "" {
			paths = append(paths, clipAppPath)
		}
	}

	// Watch
	if watchApp := result.Archive.Application.WatchApplication; watchApp != nil {
		if watchAppPath := watchApp.Path; watchAppPath != "" {
			paths = append(paths, watchAppPath)
		}
		for _, watchExt := range watchApp.Extensions {
			paths = append(paths, watchExt.Path)
		}
	}

	// Extensions
	for _, ext := range result.Archive.Application.Extensions {
		paths = append(paths, ext.Path)
	}

	return paths
}

func exportEnvironmentWithEnvman(cmdFactory command.Factory, keyStr, valueStr string) error {
	cmd := cmdFactory.Create("envman", []string{"add", "--key", keyStr}, &command.Opts{Stdin: strings.NewReader(valueStr)})
	return cmd.Run()
}

func copy(cmdFactory command.Factory, src, dst string) error {
	cmd := cmdFactory.Create("cp", []string{"-rf", src, dst}, nil)
	return cmd.Run()
}
