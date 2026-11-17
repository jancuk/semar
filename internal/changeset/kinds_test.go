package changeset

import "testing"

func TestKind(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"internal/refund/handler.go", "source"},
		{"main.go", "source"},
		{"src/app.ts", "source"},
		{"internal/refund/handler_test.go", "test"},
		{"handler_test.go", "test"},
		{"web/button.test.tsx", "test"},
		{"web/__tests__/button.tsx", "test"},
		{"internal/x/testdata/golden.json", "test"},
		{"testdata/case.yaml", "test"},
		{"README.md", "docs"},
		{"internal/x/NOTES.md", "docs"},
		{"docs/design/overview.txt", "docs"},
		{"docs/config.yaml", "docs"},
		{"testdata/readme.md", "test"},
		{"api/orders.pb.go", "generated"},
		{"internal/x/stringer_gen.go", "generated"},
		{"migrations/001_init.sql", "migration"},
		{"db/migrations/002_add.sql", "migration"},
		{"db/migrations/seed.json", "migration"},
		{"config/app.yaml", "config"},
		{".semar/scenarios/SHOP-482/SHOP-482-01.feature", "config"},
		{"service/.semar/scenarios/SHOP-482/SHOP-482-01.feature", "config"},
		{"app.yml", "config"},
		{"package.json", "config"},
		{"Cargo.toml", "config"},
		{"deployments/k8s/service.tpl", "config"},
		{".github/workflows/ci.txt", "config"},
		{"bitbucket-pipelines.yml", "config"},
		{"ci/Jenkinsfile", "config"},
		{"Dockerfile", "config"},
		{"go.mod", "source"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := Kind(c.path); got != c.want {
				t.Fatalf("Kind(%q) = %q, want %q", c.path, got, c.want)
			}
		})
	}
}
