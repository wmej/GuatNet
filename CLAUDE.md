# GuatNet 项目规则

## 打包与发布

- **打包、上传 Release、发布新版本前，先问我「要不要打包上传？」，确认后再执行**
- 不要自作主张跑 `ISCC`、创建 Release 或上传附件
- 改代码时只做编译验证：`go build -o /tmp/check.exe .`

## 版本号

- 每次改动都要升 patch 版本号（1.0.3 → 1.0.4 → …）
- 同步两处：`ui/index.html` 关于页的「版本 x.y.z」、`installer/guatnet.iss` 的 `#define MyAppVersion`
- 产物名由 Inno 自动生成：`GuatNet-v<版本>-win-x64-setup.exe`

## 构建

```bash
go build -ldflags="-s -w -H=windowsgui" -o guatnet.exe .
ISCC.exe installer\guatnet.iss        # 输出到 dist\
```

## 注意事项

- 账号密码只存在于 `%APPDATA%\guatnet\`，绝不写进代码、日志或提交
- 文档和截图里的账号一律用假号（如 `202400000000`）
- 认证协议以学校登录页实际请求为准：`/drcom/login`，运营商走 `R3`，`DDDDD`/`upass` 必须排在 `0MKKey` 之前
