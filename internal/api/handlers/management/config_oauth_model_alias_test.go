package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestOAuthModelAliasManagementPreservesTemplate(t *testing.T) {
	configPath := writeTestConfigFile(t)
	handler := &Handler{cfg: &config.Config{}, configFilePath: configPath}
	body := `{"codex":[{"name":" hidden-codex-model ","alias":" codex-latest ","template":" gpt-5.6-sol ","force-mapping":true}]}`
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/v0/management/oauth-model-alias", strings.NewReader(body))
	handler.PutOAuthModelAlias(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := handler.cfg.OAuthModelAlias["codex"][0].Template; got != "gpt-5.6-sol" {
		t.Fatalf("sanitized template = %q, want gpt-5.6-sol", got)
	}
	persisted, errRead := os.ReadFile(configPath)
	if errRead != nil {
		t.Fatalf("read persisted config: %v", errRead)
	}
	if !strings.Contains(string(persisted), "template: gpt-5.6-sol") {
		t.Fatalf("persisted YAML lost template: %s", persisted)
	}

	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/oauth-model-alias", nil)
	handler.GetOAuthModelAlias(ctx)
	var response struct {
		Aliases map[string][]config.OAuthModelAlias `json:"oauth-model-alias"`
	}
	if errDecode := json.Unmarshal(recorder.Body.Bytes(), &response); errDecode != nil {
		t.Fatalf("decode management response: %v", errDecode)
	}
	if got := response.Aliases["codex"][0].Template; got != "gpt-5.6-sol" {
		t.Fatalf("management response template = %q, want gpt-5.6-sol", got)
	}
}
