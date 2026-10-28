package tech

import (
	"reflect"
	"testing"
)

func TestClassify(t *testing.T) {
	for p, want := range map[string][]Tech{
		"cmd/footprint/main.go":                       {{"Go", Language}},
		"src/app/card.stories.tsx":                    {{"TypeScript", Language}, {"Storybook", Tooling}},
		"Api/Pay.Api.csproj":                          {{".NET", Platform}},
		"Api/Controllers/PayController.cs":            {{"C#", Language}},
		"Dockerfile":                                  {{"Docker", Tooling}},
		"build/api.Dockerfile":                        {{"Docker", Tooling}},
		"docker-compose.override.yml":                 {{"Docker", Tooling}},
		".github/workflows/ci.yml":                    {{"GitHub Actions", Tooling}},
		".github/actions/setup/action.yaml":           {{"GitHub Actions", Tooling}},
		"azure-pipelines.yml":                         {{"Azure Pipelines", Tooling}},
		".azure-pipelines/templates/build.yml":        {{"Azure Pipelines", Tooling}},
		"infra/main.tf":                               {{"Terraform", Tooling}},
		"infra/main.bicep":                            {{"Bicep", Tooling}},
		"charts/pay/Chart.yaml":                       {{"Helm", Tooling}},
		"charts/pay/templates/deployment.yaml":        {{"Helm", Tooling}},
		"deploy/k8s/service.yaml":                     {{"Kubernetes", Tooling}},
		"web/package.json":                            {{"Node.js", Platform}},
		"web/playwright.config.ts":                    {{"TypeScript", Language}, {"Playwright", Tooling}},
		"db/migrations/001_init.sql":                  {{"SQL", Language}},
		"styles/site.scss":                            {{"SCSS", Language}},
		"Views/Home/Index.cshtml":                     {{"Razor", Language}},
		"Makefile":                                    {{"Make", Tooling}},
		"README.md":                                   nil,
		"config/appsettings.json":                     nil,
		"pipelines/templates/deploy.yml":              nil,
		"docs/manifests/app.yaml":                     nil,
		"assets/logo.png":                             nil,
		"src/compose/compose-email.ts":                {{"TypeScript", Language}},
		"src/dockerfile-parser/parse.go":              {{"Go", Language}},
		"tests/cypress/e2e/login.cy.ts":               {{"TypeScript", Language}, {"Cypress", Tooling}},
		"src/actions/user.ts":                         {{"TypeScript", Language}},
		"Directory.Packages.props":                    {{".NET", Platform}},
		"scripts/deploy.ps1":                          {{"PowerShell", Language}},
		"web/src/app/app.component.html":              {{"HTML", Language}},
		"api/schema.graphql":                          {{"GraphQL", Language}},
		"proto/pay.proto":                             {{"Protocol Buffers", Language}},
		"src/values/strings.ts":                       {{"TypeScript", Language}},
		"helm/values.prod.yaml":                       {{"Helm", Tooling}},
		"Pay.sln":                                     {{".NET", Platform}},
		"tools/build.mk":                              {{"Make", Tooling}},
		".storybook/main.js":                          {{"JavaScript", Language}, {"Storybook", Tooling}},
		"src/Pay.Domain/Models/Payment.Designer.cs":   {{"C#", Language}},
		"web/src/environments/environment.prod.ts":    {{"TypeScript", Language}},
		"infra/environments/prod/terraform.tfvars":    {{"Terraform", Tooling}},
		"src/Kubernetes/KubernetesClient.cs":          {{"C#", Language}},
		"src/app/shared/components/button/button.vue": {{"Vue", Language}},
		"specs/Login.feature":                         {{"Gherkin", Language}},
		"queries/errors.kql":                          {{"KQL", Language}},
		"build/stages/deploy.yml":                     {{"Azure Pipelines", Tooling}},
		"svc/values.devtest.yaml":                     {{"Helm", Tooling}},
		"tools/BUILD.bazel":                           {{"Bazel", Tooling}},
		"BUILD":                                       {{"Bazel", Tooling}},
		"mkdocs.yml":                                  {{"MkDocs", Tooling}},
		"catalog-info.yaml":                           {{"Backstage", Tooling}},
		"Resources/Strings.resx":                      {{".NET", Platform}},
		"src/build/index.ts":                          {{"TypeScript", Language}},
	} {
		if got := Classify(p); !reflect.DeepEqual(got, want) {
			t.Errorf("Classify(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestNames(t *testing.T) {
	if got := Names("TypeScript"); !reflect.DeepEqual(got, []string{"TypeScript", "TS"}) {
		t.Errorf("Names = %v", got)
	}
	if got := Names("Rust"); !reflect.DeepEqual(got, []string{"Rust"}) {
		t.Errorf("Names without aliases = %v", got)
	}
}
