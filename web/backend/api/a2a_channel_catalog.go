package api

// init appends the fork's A2A channel to the web catalog without editing the
// upstream channel list. The matching secret field lets the UI show whether a
// token is configured without exposing it.
func init() {
	channelCatalog = append(channelCatalog, channelCatalogItem{Name: "a2a", ConfigKey: "a2a"})
	channelSecretFieldMap["a2a"] = []string{"token"}
}
