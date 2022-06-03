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

Add this step directly to your workflow in the [Bitrise Workflow Editor](https://devcenter.bitrise.io/steps-and-workflows/steps-and-workflows-index/).

You can also run this step directly with [Bitrise CLI](https://github.com/bitrise-io/bitrise).

## ⚙️ Configuration

<details>
<summary>Inputs</summary>

| Key | Description | Flags | Default |
| --- | --- | --- | --- |
| `project_path` | Xcode Project (`.xcodeproj`) or Workspace (`.xcworkspace`) path.  _xcodebuild steps:_  The input value sets xcodebuild's `-project` or `-workspace` option. | required | `$BITRISE_PROJECT_PATH` |
| `scheme` | Xcode Scheme name.  _xcodebuild steps:_  The input value sets xcodebuild's `-scheme` option. | required | `$BITRISE_SCHEME` |
| `simulator_device` | Set this exactly as it appears in the device selection menu in Xcode's device selection UI.  A couple of examples (the actual available options depend on which versions are installed):  * iPhone 8 Plus * iPhone Xs Max * iPad Air (3rd generation) * iPad Pro (12.9-inch) (3rd generation) * Apple TV 4K  Don't forget to set the platform to `tvOS Simulator` in order to use an Apple TV simulator. | required | `iPhone 8 Plus` |
| `simulator_os_version` | Set this exactly as it appears in Xcode's device selection UI.  A couple of format examples (the actual available options depend on which versions are installed):  * "8.4" * latest | required | `latest` |
| `simulator_platform` | Set this exactly as it appears in Xcode's device selection UI.  A couple of examples (the actual available options depend on which versions are installed):  * iOS Simulator * tvOS Simulator | required | `iOS` |
| `configuration` | (Optional) The name of the Xcode Configuration to use (Debug, Release, etc.). By default your Scheme's archive action defines which Configuration should be used, but this can be overridden it with this option.  **If the Configuration specified in this input does not exist in your project, the Step will silently ignore the value, and fall back to using the Configuration specified in the Scheme.** |  |  |
| `disable_index_while_building` | When this input is enabled, `COMPILER_INDEX_STORE_ENABLE=NO` is added to the `xcodebuild` command, which disables indexing while building. Disabling this could speed up your builds by eliminating a (normally) unnecessary step.  Indexing is useful for certain editor features — like autocompletion, jump to definition, and code information lookup — but these features are generally not necessary in a CI environment. |  | `yes` |
| `code_signing_allowed` | When building an app for the simulator, code signing is not required and is set to "no" by default.  On rare occasions, you may need to set the flag to "yes" — usually when working with certain test cases or third-party dependencies. |  | `no` |
| `cache_level` | Defines what cache content should be automatically collected.  Available options:  - `none`: Disable collecting cache content - `swift_packages`: Collect Swift PM packages added to the Xcode project | required | `swift_packages` |
| `xcodebuild_options` | Additional options to be added to the executed xcodebuild command. |  |  |
| `workdir` | The working directory of the Step |  | `$BITRISE_SOURCE_DIR` |
| `output_dir` | This directory will contain the generated artifacts. | required | `$BITRISE_DEPLOY_DIR` |
| `is_clean_build` | Whether or not to do a clean build before building | required | `no` |
| `output_tool` | Defines how xcodebuild command's log is formatted.  Available options:  - `xcpretty`: The xcodebuild command's output will be prettified by xcpretty. - `xcodebuild`: Only the last 20 lines of raw xcodebuild output will be visible in the build log.  The raw xcodebuild log will be exported in both cases. | required | `xcpretty` |
| `verbose_log` | If this input is set, the Step will print additional logs for debugging. | required | `no` |
</details>

<details>
<summary>Outputs</summary>

| Environment Variable | Description |
| --- | --- |
| `BITRISE_APP_DIR_PATH` | The path to the generated (and copied) app directory |
| `BITRISE_APP_DIR_PATH_LIST` | This output will include the main target app's path, plus every dependent target's app path.  The paths are separated by a `\|` (pipe) character. (Example: `/deploy109787178/sample-apps-ios-workspace-swift.app\|/deploy109787178/bitfall.sample-apps-ios-workspace-swift-watch.app`) |
| `BITRISE_XCODE_BUILD_RAW_RESULT_TEXT_PATH` | This is the path to the raw build results log file.  If `output_tool` is set to `xcpretty` and the build fails, this log will contain the build output. |
</details>

## 🙋 Contributing

We welcome [pull requests](https://github.com/bitrise-steplib/steps-xcode-build-for-simulator/pulls) and [issues](https://github.com/bitrise-steplib/steps-xcode-build-for-simulator/issues) against this repository.

For pull requests, work on your changes in a forked repository and use the Bitrise CLI to [run step tests locally](https://devcenter.bitrise.io/bitrise-cli/run-your-first-build/).

Learn more about developing steps:

- [Create your own step](https://devcenter.bitrise.io/contributors/create-your-own-step/)
- [Testing your Step](https://devcenter.bitrise.io/contributors/testing-and-versioning-your-steps/)
