---
name: mobile-app-testing
description: Drive and verify iOS and Android apps without touching the host UI - debug launch arguments, deep links and UI tests by accessibility identifier.
---

Never drive the host's UI. Clicking or typing into Simulator.app, the Android emulator window or any other
window with `osascript`, `osacompile`, `cliclick`, `xdotool`, AppleScript or JXA is denied by the mandatory
`host-ui` policy (exit 126), is invisible to reviewers and breaks when another run shares the machine. Reach a
state through the app itself instead, in this order of preference:

1. Debug launch arguments or environment that put the app into a known state (seed data, skip onboarding,
   fixed clock, open a screen). Compile them only into debug/test builds.
2. Deep links that open a screen directly.
3. UI tests that find elements by accessibility identifier (iOS) or test tag / resource id (Android), never by
   screen coordinates or visible text that copy changes would break.

Your agent's policies may deny or gate other commands the same way: a refused command exits 126 with the rule's
instruction. Follow it; for an approval rule, request `factory_owner_request_approval` for that exact argv and finish
the turn. Do not work around a guard with an absolute path or a wrapper script.

Keep screenshots, logs and test results as files under a gitignored `.build/` or `artifacts/` directory and
reference them in your report.

## iOS (simulator)

- Pick a destination from `xcrun simctl list devices available`; do not assume a device name. When you create
  a simulator for your task, name it `af-<task id>` (your Factory task id) so the node's cleanup hooks can delete
  it when the task closes; boot it with `xcrun simctl boot <udid>`.
- Launch arguments: `xcrun simctl launch <udid> <bundle-id> -seedData demo -skipOnboarding YES`. Arguments of the
  form `-key value` land in `UserDefaults.standard`; read others from `ProcessInfo.processInfo.arguments` and
  `environment`. In UI tests set `app.launchArguments` / `app.launchEnvironment` before `app.launch()`. Guard the
  handling with `#if DEBUG`.
- Deep links: register a URL scheme (Info.plist `CFBundleURLTypes`), handle it with `.onOpenURL`, and open it with
  `xcrun simctl openurl <udid> 'myapp://diary/2026-01-31'`.
- UI tests (XCUITest): give interactive views `.accessibilityIdentifier("diary.addEntry")`, find them with
  `app.buttons["diary.addEntry"]`, wait with `waitForExistence(timeout:)`. Run with
  `xcodebuild test -scheme <Scheme> -destination 'platform=iOS Simulator,id=<udid>' -resultBundlePath .build/ui.xcresult`
  (`-only-testing:<Target>/<Class>/<test>` for one test).
- Evidence: `xcrun simctl io <udid> screenshot .build/screens/diary.png`, `XCTAttachment` screenshots inside UI
  tests, the `.xcresult` bundle.
- Permission prompts and system alerts: pre-grant with `xcrun simctl privacy <udid> grant <service> <bundle-id>`
  or handle them in the UI test with `addUIInterruptionMonitor`; do not click them away on the host.

## Android (emulator)

- Pick a device from `adb devices`; prefer a headless emulator (`emulator -avd <name> -no-window -no-audio`)
  and name an AVD you create `af-<task id>`.
- Launch arguments: intent extras read only in debug builds, e.g.
  `adb shell am start -n <package>/.MainActivity --es seedData demo --ez skipOnboarding true`; in instrumented
  tests use `InstrumentationRegistry.getArguments()` (`-e key value` to `am instrument` or
  `-Pandroid.testInstrumentationRunnerArguments.key=value` to Gradle).
- Deep links: declare an intent filter with your scheme or app link and open it with
  `adb shell am start -W -a android.intent.action.VIEW -d 'myapp://diary/2026-01-31' <package>`.
- UI tests: Compose `Modifier.testTag("diary.addEntry")` with `onNodeWithTag`, Views with resource ids and
  Espresso `withId`, UiAutomator `By.res(<package>, "addEntry")` across apps. Run with
  `./gradlew connectedDebugAndroidTest` (`-Pandroid.testInstrumentationRunnerArguments.class=<Class>#<test>` for
  one test). Avoid `adb shell input tap x y`: coordinates break on another screen size.
- Evidence: `adb exec-out screencap -p > .build/screens/diary.png`, test reports under
  `app/build/reports/androidTests/`, `adb logcat -d` filtered to your package.

## When something cannot be automated

If a state truly needs a human gesture (a system dialog with no test hook, a hardware feature), add a debug
launch argument or deep link that reaches it, or say so in your report with what you verified instead. Do not
claim a manual check you did not run.
