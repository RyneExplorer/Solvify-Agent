package migrations

import _ "embed"

// ContextArtifacts is shared by startup and the repeatable upgrade script.
//
//go:embed 20261012_context_artifacts.sql
var ContextArtifacts string
