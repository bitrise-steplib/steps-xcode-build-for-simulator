package main

import (
	"os"

	"github.com/bitrise-io/go-utils/log"
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
	return NewBuildForSimulatorStep()
}
