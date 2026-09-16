package server

// museCCVersion is stamped on the upstream User-Agent. Meta does not gate on
// it (a bare probe UA returns 200 on every endpoint), so there is nothing to
// impersonate — this proxy identifies itself honestly.
const museCCVersion = "1.0.0"

// museUA returns the User-Agent sent on all upstream Meta requests.
func museUA() string {
	return "muse-cc/" + museCCVersion
}
