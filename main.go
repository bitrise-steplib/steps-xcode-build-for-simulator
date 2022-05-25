package main

import (
	"github.com/bitrise-io/go-utils/log"
	"os"
)

func main() {
	os.Exit(run())
}

func run() int {
	step := createStep()

	cfg, err := step.ProcessConfig()
	if err != nil {
		log.Errorf("Error processing config: %s", err)
		return 1
	}

	cfg, err = step.InstallDependencies(cfg)
	if err != nil {
		log.Errorf("Error installing dependencies: %s", err)
		return 1
	}

	exportOptions, err := step.Run(cfg)
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
