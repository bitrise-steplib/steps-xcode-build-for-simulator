package main

import (
	"os"

	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/v2/fileutil"
	"github.com/bitrise-io/go-utils/v2/pathutil"
)

func main() {
	os.Exit(run())
}

func run() int {
	step := createStep()

	runOpts, err := step.ProcessConfig()
	if err != nil {
		log.Errorf("Error processing config: %s", err)
		return 1
	}

	runOpts, err = step.InstallDependencies(runOpts)
	if err != nil {
		log.Errorf("Error installing dependencies: %s", err)
		return 1
	}

	exportOptions, err := step.Run(runOpts)
	if err != nil {
		log.Errorf("Error running step: %s", err)
		return 1
	}

	err = step.ExportOutput(exportOptions)
	if err != nil {
		log.Errorf("Error exporting outputs: %s", err)
		return 1
	}

	return 0
}

func createStep() BuildForSimulatorStep {
	pathProvider := pathutil.NewPathProvider()
	pathChecker := pathutil.NewPathChecker()
	pathModifier := pathutil.NewPathModifier()
	fileManager := fileutil.NewFileManager()

	return NewBuildForSimulatorStep(pathProvider, pathChecker, pathModifier, fileManager)
}
