package app

// ProtocolPayoutModulesForTest exposes the exemption list so the completeness
// test can compare it against the source-derived inventory. Exported only to
// the test build, because the list itself is an implementation detail.
func ProtocolPayoutModulesForTest() []string { return protocolPayoutModules }
