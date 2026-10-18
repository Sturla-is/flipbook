package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type toolSpec struct {
	name        string
	description string
	method      string
	path        string
	legacyPath  string
	pathParams  []string
	required    []string
	optional    []string
	freeform    bool
}

var mcpProperties = map[string]map[string]interface{}{
	"id":              {"type": "string", "description": "Flipbook ID"},
	"revision_id":     {"type": "string", "description": "Revision ID"},
	"job_id":          {"type": "string", "description": "Durable job ID"},
	"collection_id":   {"type": "string", "description": "Collection ID"},
	"link_id":         {"type": "string", "description": "Link overlay ID"},
	"lead_id":         {"type": "string", "description": "Lead ID"},
	"subscriber_id":   {"type": "string", "description": "Subscriber ID"},
	"purchase_id":     {"type": "string", "description": "Purchase ID"},
	"user_id":         {"type": "string", "description": "User ID"},
	"invitation_id":   {"type": "string", "description": "Invitation ID"},
	"key_id":          {"type": "string", "description": "API key ID"},
	"connection_id":   {"type": "string", "description": "Integration connection ID"},
	"webhook_id":      {"type": "string", "description": "Webhook subscription ID"},
	"workspace_id":    {"type": "string", "description": "Workspace ID; defaults to the API key or session workspace"},
	"idempotency_key": {"type": "string", "description": "Optional idempotency key for safe retries"},
	"title":           {"type": "string"},
	"filename":        {"type": "string", "description": "Source filename including a supported extension"},
	"file_base64":     {"type": "string", "description": "Base64-encoded source document"},
	"url":             {"type": "string"},
	"name":            {"type": "string"},
	"slug":            {"type": "string"},
	"reason":          {"type": "string"},
	"note":            {"type": "string"},
	"confirm":         {"type": "boolean", "description": "Must be true for permanent purge"},
	"action":          {"type": "string", "description": "Bulk action name"},
	"ids":             {"type": "array", "items": map[string]string{"type": "string"}},
	"tags":            {"type": "array", "items": map[string]string{"type": "string"}},
	"tag":             {"type": "string"},
	"visibility":      {"type": "string", "enum": []string{"public", "unlisted", "private", "team", "password", "token"}},
	"role":            {"type": "string", "enum": []string{"reader", "editor", "publisher", "admin", "owner"}},
	"email":           {"type": "string", "format": "email"},
	"scopes":          {"type": "array", "items": map[string]string{"type": "string"}},
	"events":          {"type": "array", "items": map[string]string{"type": "string"}},
	"enabled":         {"type": "boolean"},
	"dimension":       {"type": "string", "enum": []string{"day", "referrer", "device", "browser", "country", "placement", "utm_source", "utm_medium", "utm_campaign"}},
	"status":          {"type": "string"},
	"limit":           {"type": "integer", "minimum": 1},
	"offset":          {"type": "integer", "minimum": 0},
	"from":            {"type": "string", "description": "RFC3339 start time"},
	"to":              {"type": "string", "description": "RFC3339 end time"},
	"flipbook_id":     {"type": "string"},
	"flipbook_ids":    {"type": "array", "items": map[string]string{"type": "string"}},
	"q":               {"type": "string"},
	"publication_state": {"type": "string", "enum": []string{"draft", "published", "unpublished"}},
	"external_source": {"type": "string"},
	"external_id":     {"type": "string"},
	"archived":        {"type": "boolean"},
	"sort":            {"type": "string", "enum": []string{"created_at", "updated_at", "title", "published_at", "page_count"}},
	"order":           {"type": "string", "enum": []string{"asc", "desc"}},
	"include_pages":   {"type": "boolean"},
	"publish_at":      {"type": "string", "format": "date-time"},
	"expires_at":      {"type": "string", "format": "date-time"},
	"description":     {"type": "string"},
	"cover_flipbook_id": {"type": "string"},
	"seo":             {"type": "object", "additionalProperties": true},
	"settings":        {"type": "object", "additionalProperties": true},
	"add":             {"type": "array", "items": map[string]string{"type": "string"}},
	"remove":          {"type": "array", "items": map[string]string{"type": "string"}},
	"page":            {"type": "integer", "minimum": 1},
	"x":               {"type": "number", "minimum": 0},
	"y":               {"type": "number", "minimum": 0},
	"width":           {"type": "number", "exclusiveMinimum": 0},
	"height":          {"type": "number", "exclusiveMinimum": 0},
	"type":            {"type": "string"},
	"target_page":     {"type": "integer", "minimum": 1},
	"types":           {"type": "array", "items": map[string]string{"type": "string"}},
	"expires_in_hours": {"type": "integer", "minimum": 1},
	"resource_type":   {"type": "string"},
	"resource_id":     {"type": "string"},
	"method":          {"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE"}},
	"path":            {"type": "string", "description": "Path relative to /api/v1, for example /flipbooks"},
	"body":            {"type": "object", "additionalProperties": true},
	"query":           {"type": "object", "additionalProperties": true},
	"wait":            {"type": "boolean", "description": "Wait for conversion to finish (create defaults to true)"},
}

func spec(name, description, method, path string, pathParams []string, required []string, optional ...string) toolSpec {
	return toolSpec{name: name, description: description, method: method, path: path, pathParams: pathParams, required: required, optional: optional}
}

func (s *server) toolDefinitions() []tool {
	specs := s.toolSpecs()
	tools := make([]tool, 0, len(specs)+4)
	for _, item := range specs {
		properties := make(map[string]interface{})
		for _, name := range append(append([]string{}, item.pathParams...), item.optional...) {
			if property, ok := mcpProperties[name]; ok {
				properties[name] = property
			}
		}
		properties["workspace_id"] = mcpProperties["workspace_id"]
		if item.method != http.MethodGet {
			properties["idempotency_key"] = mcpProperties["idempotency_key"]
		}
		if item.freeform {
			properties["body"] = mcpProperties["body"]
		}
		tools = append(tools, tool{
			Name: item.name, Description: item.description,
			InputSchema: inputSchema{Type: "object", Properties: properties, Required: item.required, AdditionalProperties: item.freeform},
		})
	}
	for _, item := range []tool{
		{
			Name: "create_flipbook", Description: "Upload a PDF, PowerPoint, or supported office document, optionally with external IDs, tags, collections, and a desired slug. Waits for conversion by default.",
			InputSchema: inputSchema{Type: "object", Properties: map[string]interface{}{
				"filename": mcpProperties["filename"], "file_base64": mcpProperties["file_base64"], "title": mcpProperties["title"],
				"slug": mcpProperties["slug"], "visibility": mcpProperties["visibility"], "tags": mcpProperties["tags"],
				"workspace_id": mcpProperties["workspace_id"], "idempotency_key": mcpProperties["idempotency_key"], "wait": mcpProperties["wait"],
			}, Required: []string{"filename", "file_base64"}, AdditionalProperties: true},
		},
		{
			Name: "replace_flipbook", Description: "Upload a replacement source as a new immutable revision without taking the current publication offline.",
			InputSchema: inputSchema{Type: "object", Properties: map[string]interface{}{
				"id": mcpProperties["id"], "filename": mcpProperties["filename"], "file_base64": mcpProperties["file_base64"],
				"workspace_id": mcpProperties["workspace_id"], "idempotency_key": mcpProperties["idempotency_key"], "wait": mcpProperties["wait"],
			}, Required: []string{"id", "filename", "file_base64"}},
		},
		{
			Name: "import_google_slides", Description: "Import a publicly shared Google Slides presentation and wait for conversion by default.",
			InputSchema: inputSchema{Type: "object", Properties: map[string]interface{}{
				"url": mcpProperties["url"], "title": mcpProperties["title"], "slug": mcpProperties["slug"],
				"workspace_id": mcpProperties["workspace_id"], "idempotency_key": mcpProperties["idempotency_key"], "wait": mcpProperties["wait"],
			}, Required: []string{"url"}, AdditionalProperties: true},
		},
		{
			Name: "call_api", Description: "Call any flipbook /api/v1 endpoint with a scoped API key. Prefer named tools when available.",
			InputSchema: inputSchema{Type: "object", Properties: map[string]interface{}{
				"method": mcpProperties["method"], "path": mcpProperties["path"], "body": mcpProperties["body"], "query": mcpProperties["query"],
				"workspace_id": mcpProperties["workspace_id"], "idempotency_key": mcpProperties["idempotency_key"],
			}, Required: []string{"method", "path"}, AdditionalProperties: true},
		},
	} {
		tools = append(tools, item)
	}
	return tools
}

func (s *server) toolSpecs() []toolSpec {
	items := []toolSpec{
		spec("list_flipbooks", "Search and paginate flipbooks with lifecycle, visibility, tag, collection, and external-ID filters.", "GET", "/flipbooks", nil, nil, "q", "status", "publication_state", "visibility", "tags", "collection_id", "external_source", "external_id", "archived", "sort", "order", "limit", "offset"),
		spec("get_flipbook", "Get complete metadata, URLs, settings, and page assets for a flipbook.", "GET", "/flipbooks/{id}", []string{"id"}, []string{"id"}, "include_pages"),
		spec("get_flipbook_status", "Get conversion and active-revision status.", "GET", "/flipbooks/{id}/status", []string{"id"}, []string{"id"}),
		spec("update_flipbook", "Patch metadata, desired slug, tags, collections, SEO, branding, lead, sales, and reader settings.", "PATCH", "/flipbooks/{id}", []string{"id"}, []string{"id"}),
		spec("delete_flipbook", "Soft-delete a flipbook and schedule its permanent purge.", "DELETE", "/flipbooks/{id}", []string{"id"}, []string{"id"}),
		spec("publish_flipbook", "Publish immediately or schedule publication with visibility and expiry.", "POST", "/flipbooks/{id}/publish", []string{"id"}, []string{"id"}, "visibility", "publish_at", "expires_at"),
		spec("unpublish_flipbook", "Unpublish a flipbook and clear its publication schedule.", "POST", "/flipbooks/{id}/unpublish", []string{"id"}, []string{"id"}),
		spec("submit_flipbook_review", "Submit a ready draft for publication review.", "POST", "/flipbooks/{id}/submit-review", []string{"id"}, []string{"id"}, "note"),
		spec("approve_flipbook_review", "Approve a pending publication review.", "POST", "/flipbooks/{id}/review/approve", []string{"id"}, []string{"id"}, "note"),
		spec("reject_flipbook_review", "Reject a pending publication review.", "POST", "/flipbooks/{id}/review/reject", []string{"id"}, []string{"id"}, "note"),
		spec("archive_flipbook", "Archive a flipbook without deleting its assets.", "POST", "/flipbooks/{id}/archive", []string{"id"}, []string{"id"}, "reason"),
		spec("restore_flipbook", "Restore an archived flipbook.", "POST", "/flipbooks/{id}/restore", []string{"id"}, []string{"id"}),
		spec("purge_flipbook", "Permanently purge a flipbook and all associated artifacts. Requires confirm=true.", "POST", "/flipbooks/{id}/purge", []string{"id"}, []string{"id", "confirm"}, "confirm"),
		spec("duplicate_flipbook", "Duplicate a publication as a new draft and queue conversion from its retained original.", "POST", "/flipbooks/{id}/duplicate", []string{"id"}, []string{"id"}, "title", "slug"),
		spec("list_revisions", "List immutable revisions for a flipbook.", "GET", "/flipbooks/{id}/revisions", []string{"id"}, []string{"id"}),
		spec("get_revision", "Get one flipbook revision.", "GET", "/flipbooks/{id}/revisions/{revision_id}", []string{"id", "revision_id"}, []string{"id", "revision_id"}),
		spec("delete_revision", "Delete a non-active revision.", "DELETE", "/flipbooks/{id}/revisions/{revision_id}", []string{"id", "revision_id"}, []string{"id", "revision_id"}),
		spec("activate_revision", "Activate a validated revision.", "POST", "/flipbooks/{id}/revisions/{revision_id}/activate", []string{"id", "revision_id"}, []string{"id", "revision_id"}),
		spec("rollback_flipbook", "Roll back to an older validated revision.", "POST", "/flipbooks/{id}/revisions/{revision_id}/rollback", []string{"id", "revision_id"}, []string{"id", "revision_id"}),
		spec("retry_flipbook", "Retry or requeue flipbook conversion.", "POST", "/flipbooks/{id}/retry", []string{"id"}, []string{"id"}, "revision_id"),
		spec("cancel_flipbook", "Cancel queued or running conversion and backup jobs for a flipbook.", "POST", "/flipbooks/{id}/cancel", []string{"id"}, []string{"id"}),
		spec("bulk_flipbook_action", "Run publish, unpublish, archive, restore, delete, purge, retry, move, add_tags, or remove_tags for up to 100 flipbooks.", "POST", "/flipbooks/bulk", nil, []string{"action", "ids"}, "action", "ids", "visibility", "collection_id", "tags", "reason", "confirm"),
		spec("list_collections", "List workspace collections.", "GET", "/collections", nil, nil, "visibility"),
		spec("create_collection", "Create a collection and optionally add flipbooks.", "POST", "/collections", nil, []string{"name"}, "name", "slug", "description", "visibility", "flipbook_ids", "cover_flipbook_id", "seo"),
		spec("get_collection", "Get a collection by ID or slug.", "GET", "/collections/{collection_id}", []string{"collection_id"}, []string{"collection_id"}),
		spec("update_collection", "Patch collection metadata and membership.", "PATCH", "/collections/{collection_id}", []string{"collection_id"}, []string{"collection_id"}),
		spec("delete_collection", "Delete a collection without deleting its flipbooks.", "DELETE", "/collections/{collection_id}", []string{"collection_id"}, []string{"collection_id"}),
		spec("add_to_collection", "Add a flipbook to a collection.", "POST", "/collections/{collection_id}/flipbooks/{id}", []string{"collection_id", "id"}, []string{"collection_id", "id"}),
		spec("remove_from_collection", "Remove a flipbook from a collection.", "DELETE", "/collections/{collection_id}/flipbooks/{id}", []string{"collection_id", "id"}, []string{"collection_id", "id"}),
		spec("list_tags", "List flipbook tag counts.", "GET", "/tags", nil, nil),
		spec("update_tags", "Replace, add, or remove flipbook tags.", "PUT", "/flipbooks/{id}/tags", []string{"id"}, []string{"id"}, "tags", "add", "remove"),
		spec("delete_tag", "Remove one tag from a flipbook.", "DELETE", "/flipbooks/{id}/tags/{tag}", []string{"id", "tag"}, []string{"id", "tag"}),
		spec("list_links", "List editable link overlays for a flipbook.", "GET", "/flipbooks/{id}/links", []string{"id"}, []string{"id"}, "revision_id"),
		spec("create_link", "Create an external, internal, or video link overlay.", "POST", "/flipbooks/{id}/links", []string{"id"}, []string{"id", "page", "x", "y", "width", "height", "type"}, "revision_id", "page", "x", "y", "width", "height", "type", "url", "target_page", "title", "enabled"),
		spec("update_link", "Patch a link overlay.", "PATCH", "/flipbooks/{id}/links/{link_id}", []string{"id", "link_id"}, []string{"id", "link_id"}),
		spec("delete_link", "Delete a link overlay.", "DELETE", "/flipbooks/{id}/links/{link_id}", []string{"id", "link_id"}, []string{"id", "link_id"}),
		spec("analytics_summary", "Get privacy-aware analytics totals for a date and publication filter.", "GET", "/analytics/summary", nil, nil, "flipbook_id", "flipbook_ids", "types", "from", "to"),
		spec("analytics_breakdown", "Get analytics trends or referrer, device, country, placement, and campaign breakdowns.", "GET", "/analytics/breakdown", nil, nil, "dimension", "flipbook_id", "flipbook_ids", "types", "from", "to"),
		spec("analytics_pages", "Get per-page exposure, active time, and completion statistics.", "GET", "/analytics/pages", nil, nil, "flipbook_id", "flipbook_ids", "types", "from", "to"),
		spec("list_analytics_events", "List raw privacy-safe analytics events within retention limits.", "GET", "/analytics/events", nil, nil, "flipbook_id", "flipbook_ids", "types", "from", "to", "limit"),
		spec("list_leads", "List and paginate captured leads.", "GET", "/leads", nil, nil, "flipbook_id", "from", "to", "limit", "offset"),
		spec("delete_lead", "Delete a lead for privacy or retention management.", "DELETE", "/leads/{lead_id}", []string{"lead_id"}, []string{"lead_id"}),
		spec("list_subscribers", "List catalog subscribers by status.", "GET", "/subscribers", nil, nil, "status"),
		spec("delete_subscriber", "Delete a subscriber.", "DELETE", "/subscribers/{subscriber_id}", []string{"subscriber_id"}, []string{"subscriber_id"}),
		spec("list_purchases", "List purchases with publication, status, and date filters.", "GET", "/sales", nil, nil, "flipbook_id", "status", "from", "to", "limit", "offset"),
		spec("sales_summary", "Get gross, refunded, and net sales totals.", "GET", "/sales/summary", nil, nil, "flipbook_id", "status", "from", "to"),
		spec("get_purchase", "Get a purchase record.", "GET", "/sales/{purchase_id}", []string{"purchase_id"}, []string{"purchase_id"}),
		spec("revoke_entitlement", "Revoke paid access without falsely recording a provider refund.", "POST", "/sales/{purchase_id}/entitlement/revoke", []string{"purchase_id"}, []string{"purchase_id"}, "reason"),
		spec("restore_entitlement", "Restore access for a paid, non-refunded purchase.", "POST", "/sales/{purchase_id}/entitlement/restore", []string{"purchase_id"}, []string{"purchase_id"}),
		spec("list_jobs", "List durable conversion, backup, and lifecycle jobs.", "GET", "/jobs", nil, nil, "flipbook_id", "type", "status", "limit"),
		spec("queue_metrics", "Get queue depth, retry, dead-letter, and oldest-job metrics.", "GET", "/jobs/metrics", nil, nil),
		spec("get_job", "Get a durable job.", "GET", "/jobs/{job_id}", []string{"job_id"}, []string{"job_id"}),
		spec("retry_job", "Retry a dead or cancelled durable job.", "POST", "/jobs/{job_id}/retry", []string{"job_id"}, []string{"job_id"}),
		spec("cancel_job", "Cancel a durable job.", "POST", "/jobs/{job_id}/cancel", []string{"job_id"}, []string{"job_id"}),
		spec("list_workspaces", "List workspaces accessible to the current identity.", "GET", "/workspaces", nil, nil),
		spec("create_workspace", "Create a workspace owned by the current user.", "POST", "/workspaces", nil, []string{"name"}, "name", "slug", "settings"),
		spec("get_workspace", "Get workspace settings and branding.", "GET", "/workspaces/{workspace_id}", []string{"workspace_id"}, []string{"workspace_id"}),
		spec("update_workspace", "Update workspace settings and branding.", "PATCH", "/workspaces/{workspace_id}", []string{"workspace_id"}, []string{"workspace_id"}),
		spec("list_members", "List workspace members and roles.", "GET", "/workspaces/{workspace_id}/members", []string{"workspace_id"}, []string{"workspace_id"}),
		spec("update_member", "Add or change a workspace member role.", "PUT", "/workspaces/{workspace_id}/members/{user_id}", []string{"workspace_id", "user_id"}, []string{"workspace_id", "user_id", "role"}, "role"),
		spec("remove_member", "Remove a workspace member while protecting the last owner.", "DELETE", "/workspaces/{workspace_id}/members/{user_id}", []string{"workspace_id", "user_id"}, []string{"workspace_id", "user_id"}),
		spec("create_invitation", "Create a scoped team invitation and return its one-time token.", "POST", "/workspaces/{workspace_id}/invitations", []string{"workspace_id"}, []string{"workspace_id", "email", "role"}, "email", "role", "expires_in_hours"),
		spec("revoke_invitation", "Revoke a team invitation.", "DELETE", "/workspaces/{workspace_id}/invitations/{invitation_id}", []string{"workspace_id", "invitation_id"}, []string{"workspace_id", "invitation_id"}),
		spec("list_api_keys", "List scoped API keys without revealing their secrets.", "GET", "/workspaces/{workspace_id}/api-keys", []string{"workspace_id"}, []string{"workspace_id"}),
		spec("create_api_key", "Create an expiring, scoped API key and return its secret once.", "POST", "/workspaces/{workspace_id}/api-keys", []string{"workspace_id"}, []string{"workspace_id", "name", "scopes"}, "name", "scopes", "expires_at", "expires_in_hours"),
		spec("revoke_api_key", "Revoke an API key.", "DELETE", "/workspaces/{workspace_id}/api-keys/{key_id}", []string{"workspace_id", "key_id"}, []string{"workspace_id", "key_id"}),
		spec("list_audit_events", "List workspace audit events with resource, action, and date filters.", "GET", "/workspaces/{workspace_id}/audit", []string{"workspace_id"}, []string{"workspace_id"}, "resource_type", "resource_id", "action", "from", "to", "limit"),
		spec("list_integrations", "List external integration connection status.", "GET", "/workspaces/{workspace_id}/integrations", []string{"workspace_id"}, []string{"workspace_id"}, "user_id"),
		spec("disconnect_integration", "Disconnect an external integration and remove stored tokens.", "DELETE", "/workspaces/{workspace_id}/integrations/{connection_id}", []string{"workspace_id", "connection_id"}, []string{"workspace_id", "connection_id"}),
		spec("list_webhooks", "List scoped webhook subscriptions.", "GET", "/workspaces/{workspace_id}/webhooks", []string{"workspace_id"}, []string{"workspace_id"}),
		spec("create_webhook", "Create a signed webhook subscription and return its signing secret once.", "POST", "/workspaces/{workspace_id}/webhooks", []string{"workspace_id"}, []string{"workspace_id", "url", "events"}, "url", "events", "enabled"),
		spec("set_webhook_enabled", "Enable or disable a webhook subscription.", "PATCH", "/workspaces/{workspace_id}/webhooks/{webhook_id}", []string{"workspace_id", "webhook_id"}, []string{"workspace_id", "webhook_id", "enabled"}, "enabled"),
		spec("disable_webhook", "Disable a webhook subscription.", "DELETE", "/workspaces/{workspace_id}/webhooks/{webhook_id}", []string{"workspace_id", "webhook_id"}, []string{"workspace_id", "webhook_id"}),
		spec("list_webhook_deliveries", "Inspect webhook delivery attempts and failures.", "GET", "/workspaces/{workspace_id}/webhooks/{webhook_id}/deliveries", []string{"workspace_id", "webhook_id"}, []string{"workspace_id", "webhook_id"}, "limit"),
		spec("test_webhook", "Queue a signed test delivery for a webhook subscription.", "POST", "/workspaces/{workspace_id}/webhooks/{webhook_id}/test", []string{"workspace_id", "webhook_id"}, []string{"workspace_id", "webhook_id"}),
		spec("purge_due_flipbooks", "Permanently purge all soft-deleted flipbooks whose retention deadline has elapsed.", "POST", "/workspaces/{workspace_id}/maintenance/purge", []string{"workspace_id"}, []string{"workspace_id"}),
	}
	for index := range items {
		switch items[index].name {
		case "list_flipbooks":
			items[index].legacyPath = "/api/flipbooks"
		case "get_flipbook":
			items[index].legacyPath = "/api/flipbooks/{id}"
		case "get_flipbook_status":
			items[index].legacyPath = "/api/flipbooks/{id}/status"
		case "delete_flipbook":
			items[index].legacyPath = "/api/flipbooks/{id}"
		case "update_flipbook", "create_collection", "update_collection", "update_workspace", "create_link", "update_link":
			items[index].freeform = true
		}
	}
	return items
}

func (s *server) callTool(params toolCallParams) toolResult {
	switch params.Name {
	case "create_flipbook":
		return s.toolCreateFlipbook(params.Arguments)
	case "replace_flipbook":
		return s.toolReplaceFlipbook(params.Arguments)
	case "import_google_slides":
		return s.toolImportGoogleSlides(params.Arguments)
	case "call_api":
		return s.toolCallAPI(params.Arguments)
	}
	for _, item := range s.toolSpecs() {
		if item.name == params.Name {
			return s.callSpec(item, params.Arguments)
		}
	}
	return textError("Unknown tool: " + params.Name)
}

func (s *server) callSpec(item toolSpec, args map[string]interface{}) toolResult {
	path := item.path
	legacyPath := item.legacyPath
	for _, name := range item.pathParams {
		value, err := requiredString(args, name)
		if err != nil {
			return textError(err.Error())
		}
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(value))
		legacyPath = strings.ReplaceAll(legacyPath, "{"+name+"}", url.PathEscape(value))
	}
	options := requestOptions(args)
	options.legacyPath = legacyPath
	var body interface{}
	if item.method != http.MethodGet && item.method != http.MethodDelete {
		if raw, ok := args["body"]; ok {
			body = raw
		} else {
			payload := make(map[string]interface{})
			for key, value := range args {
				if reservedToolArgument(key) || containsString(item.pathParams, key) {
					continue
				}
				payload[key] = value
			}
			body = payload
		}
	}
	if item.method == http.MethodGet {
		query := make(url.Values)
		for key, value := range args {
			if reservedToolArgument(key) || containsString(item.pathParams, key) {
				continue
			}
			addQueryValue(query, key, value)
		}
		if encoded := query.Encode(); encoded != "" {
			path += "?" + encoded
		}
	}
	response, err := s.apiRequest(item.method, path, body, options)
	if err != nil {
		return textError(item.name + " failed: " + err.Error())
	}
	return jsonToolResult(response)
}

func reservedToolArgument(name string) bool {
	switch name {
	case "workspace_id", "idempotency_key", "body", "wait":
		return true
	default:
		return false
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func addQueryValue(query url.Values, key string, value interface{}) {
	switch typed := value.(type) {
	case []interface{}:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, fmt.Sprint(item))
		}
		query.Set(key, strings.Join(parts, ","))
	case []string:
		query.Set(key, strings.Join(typed, ","))
	default:
		query.Set(key, fmt.Sprint(value))
	}
}

func (s *server) toolCreateFlipbook(args map[string]interface{}) toolResult {
	filename, data, err := decodeFileArgument(args)
	if err != nil {
		return textError(err.Error())
	}
	fields := uploadFields(args)
	options := requestOptions(args)
	response, err := s.apiUpload("/api/v1/flipbooks", "/api/flipbooks", filename, data, fields, options)
	if err != nil {
		return textError("create_flipbook failed: " + err.Error())
	}
	return s.uploadResult(response, args, true)
}

func (s *server) toolReplaceFlipbook(args map[string]interface{}) toolResult {
	id, err := requiredString(args, "id")
	if err != nil {
		return textError(err.Error())
	}
	filename, data, err := decodeFileArgument(args)
	if err != nil {
		return textError(err.Error())
	}
	response, err := s.apiUpload("/api/v1/flipbooks/"+url.PathEscape(id)+"/replace", "", filename, data, uploadFields(args), requestOptions(args))
	if err != nil {
		return textError("replace_flipbook failed: " + err.Error())
	}
	return s.uploadResult(response, args, false)
}

func uploadFields(args map[string]interface{}) map[string]string {
	fields := make(map[string]string)
	for _, key := range []string{"title", "slug", "description", "language", "visibility", "external_source", "external_id"} {
		if value := optionalString(args, key); value != "" {
			fields[key] = value
		}
	}
	for _, key := range []string{"tags", "collection_ids"} {
		if value, ok := args[key]; ok {
			values := make(url.Values)
			addQueryValue(values, key, value)
			fields[key] = values.Get(key)
		}
	}
	if key := optionalString(args, "idempotency_key"); key != "" && fields["external_id"] == "" {
		fields["external_source"] = "idempotency"
		fields["external_id"] = key
	}
	return fields
}

func (s *server) uploadResult(response []byte, args map[string]interface{}, defaultWait bool) toolResult {
	wait := defaultWait
	if raw, ok := args["wait"].(bool); ok {
		wait = raw
	}
	if !wait {
		return jsonToolResult(response)
	}
	var created map[string]interface{}
	if err := json.Unmarshal(response, &created); err != nil {
		return textError("upload returned invalid JSON: " + err.Error())
	}
	id, _ := created["id"].(string)
	if id == "" {
		if nested, ok := created["flipbook"].(map[string]interface{}); ok {
			id, _ = nested["id"].(string)
		}
	}
	if id == "" {
		return jsonToolResult(response)
	}
	return s.pollUntilReady(id, optionalString(args, "workspace_id"))
}

func (s *server) toolImportGoogleSlides(args map[string]interface{}) toolResult {
	rawURL, err := requiredString(args, "url")
	if err != nil {
		return textError(err.Error())
	}
	fields := uploadFields(args)
	fields["url"] = rawURL
	response, err := s.apiForm("/api/v1/flipbooks/import", "/api/flipbooks/import", fields, requestOptions(args))
	if err != nil {
		return textError("import_google_slides failed: " + err.Error())
	}
	return s.uploadResult(response, args, true)
}

func (s *server) toolCallAPI(args map[string]interface{}) toolResult {
	method, err := requiredString(args, "method")
	if err != nil {
		return textError(err.Error())
	}
	method = strings.ToUpper(method)
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return textError("method must be GET, POST, PUT, PATCH, or DELETE")
	}
	path, err := requiredString(args, "path")
	if err != nil {
		return textError(err.Error())
	}
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "://") || strings.Contains(path, "..") {
		return textError("path must be relative to /api/v1 and must not contain traversal")
	}
	if rawQuery, ok := args["query"].(map[string]interface{}); ok {
		query := make(url.Values)
		for key, value := range rawQuery {
			addQueryValue(query, key, value)
		}
		if encoded := query.Encode(); encoded != "" {
			path += "?" + encoded
		}
	}
	var body interface{}
	if raw, ok := args["body"]; ok {
		body = raw
	}
	response, err := s.apiRequest(method, path, body, requestOptions(args))
	if err != nil {
		return textError("call_api failed: " + err.Error())
	}
	return jsonToolResult(response)
}
