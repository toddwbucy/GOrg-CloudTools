package tui

// This file contains stub constructors for screens that are implemented in
// subsequent PRs. Each stub delegates to newPlaceholderModel so the binary
// compiles and navigates correctly while the full screen is being built.

func newOSToolsModel(root *Model) placeholderModel {
	return newPlaceholderModel("OS Tools — coming in PR-4\n\n[Esc] Back")
}

func newCloudToolsModel(root *Model) placeholderModel {
	return newPlaceholderModel("Cloud Tools — coming in PR-4\n\n[Esc] Back")
}

func newJobHistoryModel(root *Model) placeholderModel {
	return newPlaceholderModel("Job History — coming in PR-6\n\n[Esc] Back")
}
