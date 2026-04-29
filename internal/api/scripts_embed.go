package api

import _ "embed"

//go:embed embed_connect.sh
var embedConnectSh string

//go:embed embed_approval.sh
var embedApprovalSh string

//go:embed embed_approval.ps1
var embedApprovalPs1 string
