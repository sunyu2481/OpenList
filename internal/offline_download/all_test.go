package offline_download

import (
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
)

// 黑名单里的名字必须和工具的实际注册名一致，否则 ed2k 拦截会静默失效。
// 放在本包是因为只有这里空导入了全部工具，注册表才是完整的。
func TestEd2kUnsupportedToolNamesAreRegistered(t *testing.T) {
	for _, name := range tool.Ed2kUnsupportedToolNames() {
		if _, err := tool.Tools.Get(name); err != nil {
			t.Errorf("tool %q in ed2kUnsupportedTools is not registered: %v", name, err)
		}
	}
}
