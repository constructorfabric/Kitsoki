package graphsrv

// ReadProjectGraphConfigForTest exposes the project-profile graph-block
// reader to the external test package.
func ReadProjectGraphConfigForTest(repoRoot string) projectGraphConfig {
	return readProjectGraphConfig(repoRoot)
}
