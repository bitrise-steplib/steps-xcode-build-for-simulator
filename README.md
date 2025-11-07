# Xcode Build for Simulator

[![Step changelog](https://shields.io/github/v/release/bitrise-steplib/steps-xcode-build-for-simulator?include_prereleases&label=changelog&color=blueviolet)](https://github.com/bitrise-steplib/steps-xcode-build-for-simulator/releases)

Runs `xcodebuild` commands for simulators

<details>
<summary>Description</summary>

This Step runs the `xcodebuild` command to build and deploy an app to an iOS, tvOS, or watchOS simulator. You can use this to perform quick tests of your app, or to show it off in a browser to your clients.

The Step generates the following outputs:

- `BITRISE_APP_DIR_PATH`: The path to the generated `.app` file.
- `BITRISE_APP_DIR_PATH_LIST`: The path to the generated `.app` file, and the paths to every dependent target app.
  (Paths are separated by the `|` (pipe) character.)
- `BITRISE_XCODE_BUILD_RAW_RESULT_TEXT_PATH`: The path to the raw log file for the build.

The Step also creates an `.xctestrun` file which you can use to run tests.

Make sure to include this Step after the Steps that install the necessary dependencies — such as _Run Cocoapods Install_ — in your Workflow.

### Configuring the Step

Minimum configuration:

1. In the **Project path** input, enter the path to your Xcode Project or Workspace.
  (Only necessary if you plan to use a different scheme than the one set in the `BITRISE_PROJECT_PATH` Environment Variable.)
1. In the **Scheme** input, enter the name of the Scheme you'd like to use for building your project.
  (Only necessary if you plan to use a different scheme than the one set in the `BITRISE_SCHEME` Environment Variable.)

For more configuration options, see the descriptions of other inputs in the `step.yml` or in the Workflow Editor.

### Useful links

- [Deploying an iOS app for simulators](https://devcenter.bitrise.io/en/deploying/ios-deployment/deploying-an-ios-app-for-simulators.html)

### Related Steps

- [Xcode build for testing for iOS](https://www.bitrise.io/integrations/steps/xcode-build-for-test)
- [Appetize.io deploy](https://www.bitrise.io/integrations/steps/appetize-deploy)

</details>

## 🧩 Get started

Add this step directly to your workflow in the [Bitrise Workflow Editor](https://docs.bitrise.io/en/bitrise-ci/workflows-and-pipelines/steps/adding-steps-to-a-workflow.html).

You can also run this step directly with [Bitrise CLI](https://github.com/bitrise-io/bitrise).

## ⚙️ Configuration

<details>
<summary>Inputs</summary>

| Key | Description | Flags | Default |
| --- | --- | --- | --- |
| `project_path` | Path of the Xcode Project (`.xcodeproj`) or Workspace (`.xcworkspace`)  The input value sets xcodebuild's `-project` or `-workspace` option. | required | `$BITRISE_PROJECT_PATH` |
| `scheme` | Xcode Scheme name.  The input value sets xcodebuild's `-scheme` option. | required | `$BITRISE_SCHEME` |
| `destination` | Destination specifier describes the device to use as a destination.  The input value sets xcodebuild's `-destination` option. | required | `generic/platform=iOS Simulator` |
| `xcconfig_content` | Build settings to override the project's build settings, using xcodebuild's `-xcconfig` option.  *Code signing allowed: Whether or not to allow code signing for this build* When building an app for the simulator, code signing is not required and is set to "no" by default. On rare occasions, you may need to set the flag to "yes" — usually when working with certain test cases or third-party dependencies.  You can't define `-xcconfig` option in `Additional options for the xcodebuild command` if this input is set.  If empty, no setting is changed. When set it can be either: 1.  Existing `.xcconfig` file path.      Example:      `./ios-sample/ios-sample/Configurations/Dev.xcconfig`  2.  The contents of a newly created temporary `.xcconfig` file. (This is the default.)      Build settings must be separated by newline character (`\n`).      Example:     ```     COMPILER_INDEX_STORE_ENABLE = NO     ONLY_ACTIVE_ARCH[config=Debug][sdk=*][arch=*] = YES     ``` |  | `CODE_SIGNING_ALLOWED=NO COMPILER_INDEX_STORE_ENABLE = NO` |
| `configuration` | Xcode Build Configuration.  If not specified, the default Build Configuration will be used. (Defined in the Scheme's archive action )  The input value sets xcodebuild's `-configuration` option.  **If the Configuration specified in this input does not exist in your project, the Step will silently ignore the value, and fall back to using the Configuration specified in the Scheme.** |  |  |
| `perform_clean_action` | If this input is set, `clean` xcodebuild action will be performed besides the `build` action. | required | `no` |
| `xcodebuild_options` | Additional options to be added to the executed xcodebuild command.  Prefer using `Build settings (xcconfig)` input for specifying `-xcconfig` option. You can't use both. |  |  |
| `log_formatter` | Defines how xcodebuild command's log is formatted.  Available options: - `xcpretty`: The xcodebuild command's output will be prettified by xcpretty. - `xcodebuild`: Only the last 20 lines of raw xcodebuild output will be visible in the build log.  The raw xcodebuild log will be exported in all cases. | required | `xcpretty` |
| `output_dir` | This directory will contain the generated artifacts. | required | `$BITRISE_DEPLOY_DIR` |
| `verbose_log` | If this input is set, the Step will print additional logs for debugging. | required | `no` |
</details>

<details>
<summary>Outputs</summary>

| Environment Variable | Description |
| --- | --- |
| `BITRISE_APP_DIR_PATH` | The path to the generated (and copied) app directory |
| `BITRISE_APP_DIR_PATH_LIST` | This output will include the main target app's path, plus every dependent target's app path.  The paths are separated by a `\|` (pipe) character. (Example: `/deploy109787178/sample-apps-ios-workspace-swift.app\|/deploy109787178/bitfall.sample-apps-ios-workspace-swift-watch.app`) |
| `BITRISE_XCODEBUILD_BUILD_FOR_SIMULATOR_LOG_PATH` | The file path of the raw `xcodebuild build` command log. The log is placed into the `Output directory path`.  Only set if `log_formatter` is set to `xcpretty`. |
</details>

## 🙋 Contributing

We welcome [pull requests](https://github.com/bitrise-steplib/steps-xcode-build-for-simulator/pulls) and [issues](https://github.com/bitrise-steplib/steps-xcode-build-for-simulator/issues) against this repository.

For pull requests, work on your changes in a forked repository and use the Bitrise CLI to [run step tests locally](https://docs.bitrise.io/en/bitrise-ci/bitrise-cli/running-your-first-local-build-with-the-cli.html).

Learn more about developing steps:

- [Create your own step](https://docs.bitrise.io/en/bitrise-ci/workflows-and-pipelines/developing-your-own-bitrise-step/developing-a-new-step.html)
