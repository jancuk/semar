package android

// These make the helpers of the controller reachable from the external test package.
var FindADBFor = findADB

// SetHomebrewADB replaces the places where Homebrew puts adb, and gives the function that puts them back.
func SetHomebrewADB(paths []string) func() {
	old := homebrewADB
	homebrewADB = paths
	return func() { homebrewADB = old }
}
