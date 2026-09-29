package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
)

// Permission requirements beyond the ones in internal/access.
const (
	permPublic   = "public"   // no login
	permSignedIn = "signedin" // any signed-in account (never a worker)
	permWorker   = "worker"   // a machine holding a worker key
)

// operationPermissions lists the operations people without the admin
// permission may use, with the permissions that allow each (any of them).
// Operations missing here need admin: a new endpoint is admin-only until
// it's added (testdata/permissions.txt keeps the list reviewed).
var operationPermissions = map[string][]string{
	// signing in, and one's own account
	"auth-status":               {permPublic},
	"auth-login":                {permPublic},
	"auth-setup":                {permPublic},
	"invite-get":                {permPublic},
	"invite-redeem":             {permPublic},
	"auth-oidc-login":           {permPublic},
	"auth-oidc-callback":        {permPublic},
	"auth-logout":               {permSignedIn},
	"auth-password":             {permSignedIn},
	"me-sessions":               {permSignedIn},
	"me-ui-preferences":         {permSignedIn},
	"me-ui-preferences-save":    {permSignedIn},
	"me-sessions-revoke":        {permSignedIn},
	"me-sessions-revoke-others": {permSignedIn},
	"me-library-accounts":       {permSignedIn},
	"me-library-account-save":   {permSignedIn},
	"me-library-account-delete": {permSignedIn},
	"me-notifications":          {permSignedIn},
	"me-notifications-schema":   {permSignedIn},
	"me-notifications-create":   {permSignedIn},
	"me-notifications-update":   {permSignedIn},
	"me-notifications-delete":   {permSignedIn},
	"me-notifications-test":     {permSignedIn},
	"series-follow":             {permSignedIn},
	"series-unfollow":           {permSignedIn},
	"discover":                  {permSignedIn},
	"discover-shelf":            {permSignedIn},
	"discover-thumbnail":        {permSignedIn},

	"source-priorities-list":    {access.LibraryManage},
	"source-priorities-save":    {access.LibraryManage},
	"source-priorities-inherit": {access.LibraryManage},

	// reading the library (limited to the series the group sees)
	"series-list":         {permSignedIn},
	"series-query":        {permSignedIn},
	"series-get":          {permSignedIn},
	"series-chapters":     {permSignedIn},
	"series-cover":        {permSignedIn},
	"reading-shelf":       {permSignedIn},
	"tags-list":           {permSignedIn},
	"read-chapter":        {permSignedIn},
	"read-chapter-picker": {permSignedIn},
	"read-page":           {permSignedIn},
	"read-page-bounds":    {permSignedIn},
	"read-chapter-bounds": {permSignedIn},
	"read-progress":       {permSignedIn},
	"read-time":           {permSignedIn},
	"read-mark":           {permSignedIn},
	"read-settings":       {permSignedIn},
	"read-settings-save":  {permSignedIn},
	"me-reading-stats":    {permSignedIn},
	"updates-list":        {permSignedIn},
	"read-file":           {access.Download},

	// reading apps (their own devices)
	"reading-status":       {access.Apps},
	"reading-keys":         {access.Apps},
	"reading-keys-create":  {access.Apps},
	"reading-keys-delete":  {access.Apps},
	"reading-mihon-backup": {access.Apps},

	// finding series (to add, or to request)
	"series-lookup":     {access.LibraryManage, access.RequestsCreate},
	"series-lookup-get": {access.LibraryManage, access.RequestsCreate},

	// requests (managers fulfil them with the add flow: series-add, sources-*)
	"requests-list":     {access.RequestsCreate, access.RequestsManage, access.LibraryManage},
	"requests-get":      {access.RequestsCreate, access.RequestsManage, access.LibraryManage},
	"requests-create":   {access.RequestsCreate},
	"requests-withdraw": {access.RequestsCreate},
	"requests-count":    {access.RequestsManage, access.LibraryManage},
	"requests-decline":  {access.RequestsManage, access.LibraryManage},
	"requests-link":     {access.RequestsManage, access.LibraryManage},
	"requests-delete":   {access.RequestsManage, access.LibraryManage},

	// managing the library
	"series-add":               {access.LibraryManage, access.RequestsManage},
	"previews-open":            {access.LibraryManage, access.RequestsManage, access.RequestsCreate},
	"series-add-editions":      {access.LibraryManage, access.RequestsManage},
	"series-work-update":       {access.LibraryManage},
	"series-update":            {access.LibraryManage},
	"series-delete":            {access.LibraryManage},
	"series-editor":            {access.LibraryManage},
	"series-metadata-link":     {access.LibraryManage},
	"series-metadata-refresh":  {access.LibraryManage},
	"series-refresh":           {access.LibraryManage},
	"series-rename":            {access.LibraryManage},
	"series-rename-preview":    {access.LibraryManage},
	"series-search":            {access.LibraryManage},
	"series-source-link":       {access.LibraryManage},
	"series-sources-bulk":      {access.LibraryManage},
	"series-sources-usage":     {access.LibraryManage},
	"series-sources-switch":    {access.LibraryManage},
	"series-source-unlink":     {access.LibraryManage},
	"series-source-update":     {access.LibraryManage},
	"series-source-replace":    {access.LibraryManage},
	"series-source-order":      {access.LibraryManage},
	"chapter-decision":         {access.LibraryManage},
	"chapter-restore":          {access.LibraryManage},
	"chapters-delete":          {access.LibraryManage},
	"chapters-monitor":         {access.LibraryManage},
	"queue-list":               {access.LibraryManage},
	"queue-bulk":               {access.LibraryManage},
	"queue-clear":              {access.LibraryManage},
	"queue-pause":              {access.LibraryManage},
	"queue-resume":             {access.LibraryManage},
	"queue-remove":             {access.LibraryManage},
	"queue-retry":              {access.LibraryManage},
	"history-list":             {access.LibraryManage},
	"blocklist-list":           {access.LibraryManage},
	"blocklist-delete":         {access.LibraryManage},
	"wanted-missing":           {access.LibraryManage},
	"calendar":                 {access.LibraryManage},
	"sources-list":             {access.LibraryManage, access.RequestsManage},
	"sources-search":           {access.LibraryManage, access.RequestsManage},
	"sources-quick-search":     {access.LibraryManage, access.RequestsManage},
	"sources-browse":           {access.LibraryManage, access.RequestsManage},
	"sources-manga":            {access.LibraryManage, access.RequestsManage},
	"sources-thumbnail":        {access.LibraryManage, access.RequestsManage},
	"catalogs-list":            {access.LibraryManage, access.RequestsManage},
	"catalogs-health":          {access.LibraryManage},
	"rootfolders-list":         {access.LibraryManage, access.RequestsManage},
	"rootfolders-for-language": {access.LibraryManage, access.RequestsManage},
	"profiles-list":            {access.LibraryManage, access.RequestsManage},
	"tags-create":              {access.LibraryManage},
	"tags-delete":              {access.LibraryManage},
	"modules-asset":            {access.LibraryManage},
	"commands-push":            {access.LibraryManage}, // non-admins: managerCommands only
	"commands-list":            {access.LibraryManage},
	"commands-get":             {access.LibraryManage},

	// the worker protocol: a worker key and nothing else
	"worker-hello":     {permWorker},
	"worker-lease":     {permWorker},
	"worker-page":      {permWorker},
	"worker-input":     {permWorker},
	"worker-output":    {permWorker},
	"worker-heartbeat": {permWorker},
	"worker-complete":  {permWorker},
	"worker-fail":      {permWorker},
	"worker-bye":       {permWorker},
}

// permissionsFor returns what an operation needs (admin when unlisted).
func permissionsFor(op string) []string {
	if p, ok := operationPermissions[op]; ok {
		return p
	}
	return []string{access.Admin}
}

// allowed reports whether p may use an operation that needs any of need.
func allowed(p *access.Principal, need []string) bool {
	for _, n := range need {
		switch {
		case n == permPublic:
			return true
		case n == permSignedIn:
			// a worker is not a signed-in account: its key opens the worker
			// endpoints and nothing else
			if p != nil && p.Kind != access.KindWorker {
				return true
			}
		case n == permWorker:
			if p != nil && p.Kind == access.KindWorker {
				return true
			}
		case p.Can(n):
			return true
		}
	}
	return false
}

// requirePermission enforces operationPermissions.
func (s *Server) requirePermission(ctx huma.Context, next func(huma.Context)) {
	need := permissionsFor(ctx.Operation().OperationID)
	p := access.From(ctx.Context())
	if allowed(p, need) {
		next(ctx)
		return
	}
	if p == nil {
		_ = huma.WriteErr(s.api, ctx, http.StatusUnauthorized, "login or X-Api-Key required")
		return
	}
	_ = huma.WriteErr(s.api, ctx, http.StatusForbidden, "your account can't do this ("+strings.Join(need, " or ")+" needed)")
}

// PermissionTable lists every operation with what it needs, sorted (for
// the golden test and docs).
func PermissionTable(api huma.API) []string {
	var out []string
	for path, item := range api.OpenAPI().Paths {
		for method, op := range map[string]*huma.Operation{"GET": item.Get, "POST": item.Post, "PUT": item.Put, "PATCH": item.Patch, "DELETE": item.Delete} {
			if op == nil {
				continue
			}
			out = append(out, op.OperationID+" "+method+" "+path+" -> "+strings.Join(permissionsFor(op.OperationID), " | "))
		}
	}
	sort.Strings(out)
	return out
}
