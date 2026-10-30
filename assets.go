// Package agentlab contains the installation assets shipped with the client.
package agentlab

import "embed"

// InstallationAssets includes product scripts and the maintained agent skill.
// Development tools and test fixtures are excluded from the client payload.
//
//go:embed scripts/install_host.sh scripts/create_template.sh scripts/net scripts/guest scripts/systemd scripts/tests/golden_path.sh skills/agentlab
var InstallationAssets embed.FS
