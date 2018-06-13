package helper

import (
	"fmt"

	"github.com/bitrise-io/go-utils/command/rubyscript"
)

// TargetMapping ...
type TargetMapping struct {
	Error string `json:"error"`
	Data  string `json:"data"`
}

// Ruby ...
func Ruby(projectPth, appName string) (TargetMapping, error) {
	runner := rubyscript.New(defaultConfigScriptContent)

	bundleInstallCmd, err := runner.BundleInstallCommand(gemfileContent, "")
	if err != nil {
		return TargetMapping{}, fmt.Errorf("failed to create bundle install command, error: %s", err)
	}

	if out, err := bundleInstallCmd.RunAndReturnTrimmedCombinedOutput(); err != nil {
		return TargetMapping{}, fmt.Errorf("bundle install failed, output: %s, error: %s", out, err)
	}

	runCmd, err := runner.RunScriptCommand()
	if err != nil {
		return TargetMapping{}, fmt.Errorf("failed to create script runner command, error: %s", err)
	}

	envsToAppend := []string{
		"PROEJECTPATH=" + projectPth,
		"APP_NAME=" + appName}
	envs := append(runCmd.GetCmd().Env, envsToAppend...)

	runCmd.SetEnvs(envs...)

	out, err := runCmd.RunAndReturnTrimmedCombinedOutput()
	if err != nil {
		return TargetMapping{}, fmt.Errorf("failed to run code signing analyzer script, output: %s, error: %s", out, err)
	}

	fmt.Printf("\n\nOUT: %s\n\n", out)
	return TargetMapping{}, nil
}
