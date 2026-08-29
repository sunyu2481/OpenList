package tool

import (
	"context"
	"net/url"
	stdpath "path"
	"path/filepath"
	"strings"

	_115 "github.com/OpenListTeam/OpenList/v4/drivers/115"
	_115_open "github.com/OpenListTeam/OpenList/v4/drivers/115_open"
	_123 "github.com/OpenListTeam/OpenList/v4/drivers/123"
	_123_open "github.com/OpenListTeam/OpenList/v4/drivers/123_open"
	"github.com/OpenListTeam/OpenList/v4/drivers/guangyapan"
	"github.com/OpenListTeam/OpenList/v4/drivers/pikpak"
	"github.com/OpenListTeam/OpenList/v4/drivers/thunder"
	"github.com/OpenListTeam/OpenList/v4/drivers/thunder_browser"
	"github.com/OpenListTeam/OpenList/v4/drivers/thunderx"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/google/uuid"
	"github.com/pkg/errors"
)

type DeletePolicy string

const (
	DeleteOnUploadSucceed DeletePolicy = "delete_on_upload_succeed"
	DeleteOnUploadFailed  DeletePolicy = "delete_on_upload_failed"
	DeleteNever           DeletePolicy = "delete_never"
	DeleteAlways          DeletePolicy = "delete_always"
	UploadDownloadStream  DeletePolicy = "upload_download_stream"
)

type AddURLArgs struct {
	URL          string
	DstDirPath   string
	Tool         string
	DeletePolicy DeletePolicy
}

func AddURL(ctx context.Context, args *AddURLArgs) (task.TaskExtensionInfo, error) {
	// check storage
	storage, dstDirActualPath, err := op.GetStorageAndActualPath(args.DstDirPath)
	if err != nil {
		return nil, errors.WithMessage(err, "failed get storage")
	}
	// check is it could upload
	if storage.Config().NoUpload {
		return nil, errors.WithStack(errs.UploadNotSupported)
	}
	// check path is valid
	obj, err := op.Get(ctx, storage, dstDirActualPath)
	if err != nil {
		if !errs.IsObjectNotFound(err) {
			return nil, errors.WithMessage(err, "failed get object")
		}
	} else {
		if !obj.IsDir() {
			// can't add to a file
			return nil, errors.WithStack(errs.NotFolder)
		}
	}
	scheme := urlScheme(args.URL)

	// ed2k 由少数工具支持，不支持的直接报错，不静默改用其他工具——
	// 用户选定的工具决定了任务落到哪个网盘，替换会带来意料之外的转存。
	if scheme == ed2kScheme {
		if _, unsupported := ed2kUnsupportedTools[args.Tool]; unsupported {
			return nil, errs.NewErr(errs.NotSupport,
				"%s does not support ed2k links, please use Thunder/ThunderX/ThunderBrowser or PikPak", args.Tool)
		}
	}

	// try putting url
	if args.Tool == "SimpleHttp" {
		if scheme != "" && scheme != "http" && scheme != "https" {
			return nil, errs.NewErr(errs.NotSupport,
				"SimpleHttp does not support %s links, please use aria2 or another download tool", scheme)
		}
		err = tryPutUrl(ctx, args.DstDirPath, args.URL)
		if err == nil || !errors.Is(err, errs.NotImplement) {
			return nil, err
		}
		// Fallback to creating a download task when storage lacks native PutURL support.
	}

	// get tool
	tool, err := Tools.Get(args.Tool)
	if err != nil {
		return nil, errors.Wrapf(err, "failed get offline download tool")
	}
	// check tool is ready
	if !tool.IsReady() {
		// try to init tool
		if _, err := tool.Init(); err != nil {
			return nil, errors.Wrapf(err, "failed init offline download tool %s", args.Tool)
		}
	}

	uid := uuid.NewString()
	tempDir := filepath.Join(conf.Conf.TempDir, args.Tool, uid)
	deletePolicy := args.DeletePolicy

	// 如果当前 storage 是对应网盘，则直接下载到目标路径，无需转存
	switch args.Tool {
	case "115 Cloud":
		if _, ok := storage.(*_115.Pan115); ok {
			tempDir = args.DstDirPath
		} else {
			tempDir = filepath.Join(setting.GetStr(conf.Pan115TempDir), uid)
		}
	case "115 Open":
		if _, ok := storage.(*_115_open.Open115); ok {
			tempDir = args.DstDirPath
		} else {
			tempDir = filepath.Join(setting.GetStr(conf.Pan115OpenTempDir), uid)
		}
	case "123 Open":
		if _, ok := storage.(*_123_open.Open123); ok && dstDirActualPath != "/" {
			// directly offline downloading to the root path is not allowed via 123 open platform
			tempDir = args.DstDirPath
		} else {
			tempDir = filepath.Join(setting.GetStr(conf.Pan123OpenTempDir), uid)
		}
	case "123Pan":
		if _, ok := storage.(*_123.Pan123); ok {
			tempDir = args.DstDirPath
		} else {
			tempDir = filepath.Join(setting.GetStr(conf.Pan123TempDir), uid)
		}
	case "PikPak":
		if _, ok := storage.(*pikpak.PikPak); ok {
			tempDir = args.DstDirPath
		} else {
			tempDir = filepath.Join(setting.GetStr(conf.PikPakTempDir), uid)
		}
	case "Thunder":
		if _, ok := storage.(*thunder.Thunder); ok {
			tempDir = args.DstDirPath
		} else {
			tempDir = filepath.Join(setting.GetStr(conf.ThunderTempDir), uid)
		}
	case "ThunderBrowser":
		switch storage.(type) {
		case *thunder_browser.ThunderBrowser, *thunder_browser.ThunderBrowserExpert:
			tempDir = args.DstDirPath
		default:
			tempDir = filepath.Join(setting.GetStr(conf.ThunderBrowserTempDir), uid)
		}
	case "ThunderX":
		if _, ok := storage.(*thunderx.ThunderX); ok {
			tempDir = args.DstDirPath
		} else {
			tempDir = filepath.Join(setting.GetStr(conf.ThunderXTempDir), uid)
		}
	case "GuangYaPan":
		if _, ok := storage.(*guangyapan.GuangYaPan); ok {
			tempDir = args.DstDirPath
		} else {
			tempBase := setting.GetStr(conf.GuangYaPanTempDir)
			if tempBase == "" {
				return nil, errors.New("GuangYaPan temp dir is not set")
			}
			tempDir = filepath.Join(tempBase, uid)
		}
	}

	taskCreator, _ := ctx.Value(conf.UserKey).(*model.User) // taskCreator is nil when convert failed
	t := &DownloadTask{
		TaskExtension: task.TaskExtension{
			Creator: taskCreator,
			ApiUrl:  common.GetApiUrl(ctx),
		},
		Url:          args.URL,
		DstDirPath:   args.DstDirPath,
		TempDir:      tempDir,
		DeletePolicy: deletePolicy,
		Toolname:     args.Tool,
		tool:         tool,
	}
	DownloadTaskManager.Add(t)
	return t, nil
}

func tryPutUrl(ctx context.Context, path, urlStr string) error {
	var dstName string
	u, err := url.Parse(urlStr)
	if err == nil {
		dstName = stdpath.Base(u.Path)
	} else {
		dstName = "UnnamedURL"
	}
	return fs.PutURL(ctx, path, dstName, urlStr)
}

const ed2kScheme = "ed2k"

// urlScheme 取出 URL 的小写 scheme，无法识别时返回空串。
// 不能直接用 url.Parse：ed2k 链接形如 ed2k://|file|name|size|hash|/，
// 其中的 "|" 会让 url.Parse 报 invalid character in host name。
func urlScheme(urlStr string) string {
	urlStr = strings.TrimSpace(urlStr)
	i := strings.Index(urlStr, ":")
	if i <= 0 {
		return ""
	}
	scheme := strings.ToLower(urlStr[:i])
	// RFC 3986: scheme = ALPHA *( ALPHA / DIGIT / "+" / "-" / "." )
	for j := 0; j < len(scheme); j++ {
		c := scheme[j]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9', c == '+', c == '-', c == '.':
			if j == 0 {
				return ""
			}
		default:
			return ""
		}
	}
	return scheme
}

// toolNameForStorage 返回 storage 对应的原生离线下载工具名，没有则返回空串。
func toolNameForStorage(storage driver.Driver) string {
	switch storage.(type) {
	case *_115.Pan115:
		return "115 Cloud"
	case *_115_open.Open115:
		return "115 Open"
	case *_123.Pan123:
		return "123Pan"
	case *_123_open.Open123:
		return "123 Open"
	case *guangyapan.GuangYaPan:
		return "GuangYaPan"
	case *pikpak.PikPak:
		return "PikPak"
	case *thunder.Thunder:
		return "Thunder"
	case *thunderx.ThunderX:
		return "ThunderX"
	case *thunder_browser.ThunderBrowser, *thunder_browser.ThunderBrowserExpert:
		return "ThunderBrowser"
	default:
		return ""
	}
}

// ed2kUnsupportedTools 列出确定不支持 ed2k 的工具。
// 采用黑名单而非白名单：其余工具把链接透传给各自的网盘服务端，
// 由服务端判断能否处理，新增网盘工具时无需回来改这里。
var ed2kUnsupportedTools = map[string]struct{}{
	"SimpleHttp":   {},
	"aria2":        {},
	"qBittorrent":  {},
	"Transmission": {},
}

// Ed2kUnsupportedToolNames 返回不支持 ed2k 的工具名，供测试校验名字与注册名一致。
func Ed2kUnsupportedToolNames() []string {
	names := make([]string, 0, len(ed2kUnsupportedTools))
	for name := range ed2kUnsupportedTools {
		names = append(names, name)
	}
	return names
}
