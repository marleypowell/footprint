// Package tech names the technologies a file is written in or for, from its path alone: languages
// by extension, and tools and platforms by the files they read (Dockerfile, a workflow, a project
// file). Its tables are compiled in, so the same path always names the same technologies.
package tech

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// Categories.
const (
	Language = "language" // what code is written in: Go, TypeScript, SQL, SCSS
	Tooling  = "tooling"  // what builds, ships or tests it: Docker, GitHub Actions, Terraform, Storybook
	Platform = "platform" // what it runs on or is built for: .NET, Node.js
)

// Tech is a technology, by its canonical name.
type Tech struct {
	Name     string
	Category string
}

var languages = map[string]string{
	".go": "Go", ".ts": "TypeScript", ".tsx": "TypeScript", ".mts": "TypeScript", ".cts": "TypeScript",
	".js": "JavaScript", ".jsx": "JavaScript", ".mjs": "JavaScript", ".cjs": "JavaScript",
	".cs": "C#", ".fs": "F#", ".vb": "Visual Basic", ".py": "Python", ".java": "Java", ".kt": "Kotlin", ".kts": "Kotlin",
	".rb": "Ruby", ".php": "PHP", ".rs": "Rust", ".swift": "Swift", ".scala": "Scala", ".c": "C", ".h": "C",
	".cpp": "C++", ".cc": "C++", ".cxx": "C++", ".hpp": "C++", ".m": "Objective-C", ".dart": "Dart",
	".sql": "SQL", ".ps1": "PowerShell", ".psm1": "PowerShell", ".psd1": "PowerShell", ".sh": "Shell", ".bash": "Shell",
	".html": "HTML", ".htm": "HTML", ".cshtml": "Razor", ".razor": "Razor", ".css": "CSS", ".scss": "SCSS", ".sass": "SCSS",
	".less": "Less", ".vue": "Vue", ".svelte": "Svelte", ".graphql": "GraphQL", ".gql": "GraphQL", ".proto": "Protocol Buffers",
	".tf": "Terraform", ".tfvars": "Terraform", ".bicep": "Bicep", ".bicepparam": "Bicep", ".lua": "Lua", ".r": "R",
	".feature": "Gherkin", ".kql": "KQL", ".kusto": "KQL",
}

// tools match a lower-case path; the first rule of each name that matches counts.
var tools = []struct {
	name, category string
	re             *regexp.Regexp
}{
	{"Docker", Tooling, regexp.MustCompile(`(^|/)(dockerfile[^/]*|[^/]+\.dockerfile|\.dockerignore|(docker-)?compose[^/]*\.ya?ml)$`)},
	{"GitHub Actions", Tooling, regexp.MustCompile(`(^|/)\.github/(workflows/[^/]+|actions/.+/action)\.ya?ml$|(^|/)action\.ya?ml$`)},
	// stages/, jobs/ and steps/ of YAML are Azure Pipelines templates, by its vocabulary
	{"Azure Pipelines", Tooling, regexp.MustCompile(`(^|/)(azure-pipelines[^/]*\.ya?ml|\.?azure-?pipelines/.+\.ya?ml|\.azuredevops/.+\.ya?ml|(stages|jobs|steps)/[^/]+\.ya?ml)$`)},
	{"GitLab CI", Tooling, regexp.MustCompile(`(^|/)\.gitlab-ci\.ya?ml$`)},
	{"Jenkins", Tooling, regexp.MustCompile(`(^|/)jenkinsfile[^/]*$`)},
	{"Helm", Tooling, regexp.MustCompile(`(^|/)(chart|values(\.[^/]+)?)\.ya?ml$|(^|/)(charts?|helm)/.+\.(ya?ml|tpl)$`)},
	{"Kubernetes", Tooling, regexp.MustCompile(`(^|/)(k8s|kubernetes|kustomize)/.+\.ya?ml$|(^|/)kustomization\.ya?ml$`)},
	{"Storybook", Tooling, regexp.MustCompile(`\.stories\.(ts|tsx|js|jsx|mdx)$|(^|/)\.storybook/`)},
	{"Playwright", Tooling, regexp.MustCompile(`(^|/)playwright\.config\.[^/]+$|\.pw\.(ts|js)$`)},
	{"Cypress", Tooling, regexp.MustCompile(`(^|/)cypress(/|\.config\.[^/]+$)`)},
	{"Bazel", Tooling, regexp.MustCompile(`\.(bazel|bzl)$|(^|/)(build|workspace)$`)},
	{"MkDocs", Tooling, regexp.MustCompile(`(^|/)mkdocs\.ya?ml$`)},
	{"Backstage", Tooling, regexp.MustCompile(`(^|/)catalog-info\.ya?ml$`)},
	{"Make", Tooling, regexp.MustCompile(`(^|/)(makefile|[^/]+\.mk)$`)},
	{".NET", Platform, regexp.MustCompile(`\.(csproj|fsproj|vbproj|sln|slnx|resx)$|(^|/)(directory\.(build|packages)\.(props|targets)|global\.json|nuget\.config)$`)},
	{"Node.js", Platform, regexp.MustCompile(`(^|/)(package\.json|\.nvmrc|\.npmrc)$`)},
}

// Classify names the technologies of a file, by its path ("/"-separated, as git writes it). A file
// with none (a README, an image, plain JSON or YAML) names none.
func Classify(p string) []Tech {
	lower := strings.ToLower(p)
	var out []Tech
	if name, ok := languages[path.Ext(lower)]; ok {
		out = append(out, Tech{Name: name, Category: Language})
	}
	for _, t := range tools {
		if t.re.MatchString(lower) && !slices.ContainsFunc(out, func(o Tech) bool { return o.Name == t.name }) {
			out = append(out, Tech{Name: t.name, Category: t.category})
		}
	}
	for i := range out { // Terraform and Bicep files are infrastructure tooling, though they are languages too
		if out[i].Name == "Terraform" || out[i].Name == "Bicep" {
			out[i].Category = Tooling
		}
	}
	return out
}

// aliases are other names writing uses for a technology.
var aliases = map[string][]string{
	"TypeScript": {"TS"}, "JavaScript": {"JS"}, "Go": {"Golang"}, ".NET": {"dotnet", ".NET Core", "ASP.NET"},
	"C#": {"CSharp"}, "Node.js": {"Node", "NodeJS"}, "Kubernetes": {"K8s"}, "GitHub Actions": {"GH Actions"},
	"Protocol Buffers": {"protobuf", "Protobuf"}, "SCSS": {"Sass"},
}

// Names are every name a technology goes by in writing: its canonical name, then its aliases.
func Names(name string) []string { return append([]string{name}, aliases[name]...) }
