// Copyright 2026 Candace Labs

package githubgen_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/tools/githubgen"
)

// description is a small OpenAPI 3.0 document in GitHub's shape: two kept
// operations, one dropped, a shared component, an unreached one and an
// inline list response.
const description = `{
  "openapi": "3.0.3",
  "info": {"title": "GitHub v3 REST API", "version": "1.1.4"},
  "servers": [{"url": "https://api.github.com"}],
  "tags": [{"name": "issues"}],
  "paths": {
    "/repos/{owner}/{repo}/issues": {
      "get": {
        "operationId": "issues/list-for-repo", "summary": "List repository issues",
        "parameters": [{"$ref": "#/components/parameters/owner"}, {"name": "repo", "in": "path", "required": true, "schema": {"type": "string"}},
                       {"name": "state", "in": "query", "schema": {"type": "string"}}],
        "responses": {
          "200": {"description": "OK", "content": {"application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/issue"}}, "examples": {"default": {"value": []}}}}},
          "404": {"$ref": "#/components/responses/not_found"}
        }
      },
      "post": {
        "operationId": "issues/create", "summary": "Create an issue", "description": "Any user with pull access can create an issue.\n\nMore.",
        "parameters": [{"$ref": "#/components/parameters/owner"}, {"name": "repo", "in": "path", "required": true, "schema": {"type": "string"}}],
        "requestBody": {"required": true, "content": {"application/json": {"schema": {"type": "object", "properties": {"title": {"type": "string"}}}}}},
        "responses": {"201": {"description": "Created", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/issue"}}}}}
      }
    },
    "/repos/{owner}/{repo}/issues/{issue_number}": {
      "get": {"operationId": "issues/get", "responses": {"200": {"description": "OK"}}}
    }
  },
  "components": {
    "parameters": {"owner": {"name": "owner", "in": "path", "required": true, "description": "The account owner. More.", "schema": {"type": "string"}}},
    "schemas": {
      "issue": {"type": "object", "properties": {"number": {"type": "integer"}, "user": {"$ref": "#/components/schemas/user"}, "example": {"type": "string"}}, "example": {"number": 1}},
      "user": {"type": "object", "properties": {"login": {"type": "string"}}},
      "unreached": {"type": "string"}
    },
    "responses": {"not_found": {"description": "Not found"}}
  }
}`

var _ = Describe("The GitHub description filter", func() {
	filter := func(operations ...string) map[string]any {
		GinkgoHelper()
		filtered, err := githubgen.Filter([]byte(description), operations)
		Expect(err).NotTo(HaveOccurred())
		var document map[string]any
		Expect(json.Unmarshal(filtered, &document)).To(Succeed())
		return document
	}

	It("keeps the allowlisted operations, their success responses and the components they reach", func() {
		document := filter("issues/list-for-repo", "issues/create")
		Expect(document).To(HaveKey("servers"))
		Expect(document).NotTo(HaveKey("tags"))
		paths := document["paths"].(map[string]any)
		Expect(paths).To(HaveLen(1))
		item := paths["/repos/{owner}/{repo}/issues"].(map[string]any)
		Expect(item).To(HaveKey("get"))
		Expect(item).To(HaveKey("post"))
		Expect(item["get"].(map[string]any)["responses"]).To(HaveLen(1))
		components := document["components"].(map[string]any)
		Expect(components["schemas"]).To(SatisfyAll(HaveKey("issue"), HaveKey("user"), HaveKey("issues-list-for-repo-result"), Not(HaveKey("unreached"))))
		Expect(components["parameters"]).To(HaveKey("owner"))
		Expect(components).NotTo(HaveKey("responses"))
	})

	It("hoists an inline success schema into a named component and drops examples but not properties named example", func() {
		document := filter("issues/list-for-repo")
		get := document["paths"].(map[string]any)["/repos/{owner}/{repo}/issues"].(map[string]any)["get"].(map[string]any)
		media := get["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
		Expect(media).To(Equal(map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/issues-list-for-repo-result"}}))
		issue := document["components"].(map[string]any)["schemas"].(map[string]any)["issue"].(map[string]any)
		Expect(issue).NotTo(HaveKey("example"))
		Expect(issue["properties"]).To(HaveKey("example"))
	})

	It("writes the same bytes for the same input", func() {
		first, err := githubgen.Filter([]byte(description), []string{"issues/create", "issues/list-for-repo"})
		Expect(err).NotTo(HaveOccurred())
		second, err := githubgen.Filter([]byte(description), []string{"issues/list-for-repo", "issues/create"})
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(Equal(first))
	})

	It("refuses an operation the description does not have", func() {
		_, err := githubgen.Filter([]byte(description), []string{"issues/create", "issues/teleport"})
		Expect(err).To(MatchError(githubgen.ErrUnknownOperation))
		Expect(err).To(MatchError(ContainSubstring("issues/teleport")))
	})

	It("reads an allowlist and lists a filtered description's operations", func() {
		operations := githubgen.ReadAllowlist([]byte("# comment\n\nissues/create\n  issues/list-for-repo  \n"))
		Expect(operations).To(Equal([]string{"issues/create", "issues/list-for-repo"}))
		filtered, err := githubgen.Filter([]byte(description), operations)
		Expect(err).NotTo(HaveOccurred())
		listed, err := githubgen.Operations(filtered)
		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(Equal([]string{"issues/create", "issues/list-for-repo"}))
	})
})

var _ = Describe("The GitHub tool generator", func() {
	DescribeTable("names a tool and oapi-codegen's client method stem from an operationId",
		func(operation string, tool string, client string) {
			Expect(githubgen.ToolName(operation)).To(Equal(tool))
			Expect(githubgen.ClientName(operation)).To(Equal(client))
		},
		Entry("one word", "issues/create", "IssuesCreate", "Issuescreate"),
		Entry("a dashed verb", "issues/create-comment", "IssuesCreateComment", "IssuescreateComment"),
		Entry("a dashed noun", "repos/get-commit", "ReposGetCommit", "ReposgetCommit"),
	)

	It("writes each operation's input, handler and registration from the filtered description", func() {
		filtered, err := githubgen.Filter([]byte(description), []string{"issues/list-for-repo", "issues/create"})
		Expect(err).NotTo(HaveOccurred())
		generated, err := githubgen.Tools(filtered)
		Expect(err).NotTo(HaveOccurred())
		source := string(generated)
		Expect(source).To(HavePrefix("// Code generated by tools/githubgen"))
		Expect(source).To(ContainSubstring(`ToolIssuesCreate      = "IssuesCreate"`))
		Expect(source).To(ContainSubstring("Owner  string                          `json:\"owner\" jsonschema:\"The account owner\"`"))
		Expect(source).To(ContainSubstring("Params *github.IssueslistForRepoParams"))
		Expect(source).To(ContainSubstring("Body  github.IssuescreateJSONRequestBody `json:\"body\""))
		Expect(source).To(ContainSubstring("(ListOutput, error)"))
		Expect(source).To(ContainSubstring("tools.client.IssuescreateWithResponse(ctx, input.Owner, input.Repo, input.Body)"))
		Expect(source).To(ContainSubstring("response.JSON201 != nil"))
		Expect(source).To(ContainSubstring(`"Create an issue. Any user with pull access can create an issue. GitHub REST operation issues/create."`))
		Expect(source).To(ContainSubstring(`"List repository issues. GitHub REST operation issues/list-for-repo."`))
	})

	It("refuses an operation with no named JSON success response", func() {
		_, err := githubgen.Tools([]byte(`{"paths": {"/x": {"get": {"operationId": "x/get", "responses": {"204": {"description": "No content"}}}}}}`))
		Expect(err).To(MatchError(ContainSubstring("x/get has no named JSON success response")))
	})
})
