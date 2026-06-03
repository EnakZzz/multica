# Multica Review Capture

Multica 浏览器批注插件。支持 Chrome / Edge Manifest V3，可以在任意普通网页上用铅笔圈注问题区域、填写修改意见，并提交为 Multica 指定 workspace/project 下的新 issue。

## 构建

在仓库根目录执行：

```powershell
pnpm --filter @multica/browser-extension build
```

构建后的 unpacked extension 位于：

```text
apps/browser-extension/dist
```

本机开发调试时，可以直接在浏览器里加载这个目录。

## 打包分发

在仓库根目录执行：

```powershell
pnpm --filter @multica/browser-extension package
```

脚本会先构建插件，然后生成带版本号的 zip：

```text
apps/browser-extension/releases/multica-review-extension-<version>.zip
```

版本号来自：

```text
apps/browser-extension/public/manifest.json
```

发给同事时，只需要发 `releases/` 下的 zip。

## 安装

1. 解压插件 zip。
2. 打开 `edge://extensions/` 或 `chrome://extensions/`。
3. 开启开发者模式。
4. 点击 `Load unpacked` / `加载已解压的扩展程序`。
5. 选择解压后的插件目录。
6. 确认浏览器扩展页显示的版本号和 zip 版本一致。

如果是本机开发，也可以直接加载：

```text
apps/browser-extension/dist
```

## 更新

1. 获取新的插件 zip。
2. 解压到一个干净目录。
3. 打开浏览器扩展页。
4. 对 `Multica Review Capture` 点击 reload / 重新加载。
5. 刷新已经打开的被测试网页。

如果 reload 后仍然是旧行为，先删除旧扩展，再重新 `Load unpacked` 新目录。

## 配置

打开插件 popup，填写 Multica 前端地址，例如：

```text
http://10.160.108.87:3001
```

然后：

1. 点击 `Sign in` 登录 Multica。
2. 登录完成后回到插件，点击 `Refresh`。
3. 选择 workspace 和 project。

选择 project 后会自动保存，不需要额外点击保存按钮。后续再次打开插件会保留上次选择。

如需切换 Multica 身份，点击 `Sign out`，然后重新 `Sign in`。

## 使用

1. 打开要审阅的网页。
2. 点击插件里的 `Annotate page`，或使用快捷键 `Alt+Shift+M`。
3. 用铅笔圈住问题区域。
4. 在右侧面板填写修改意见。
5. 如不需要圈区域，可点击 `Add page note` 直接写页面级问题。
6. 点击 `Submit issue`。

提交后会创建一个新的 Multica issue，并上传：

- 当前视口截图。
- 页面状态和批注信息 JSON。

截图时会隐藏插件工具条、右侧面板和遮罩，但会保留铅笔圈注。

## 限制

- 插件不能在 `edge://`、`chrome://`、浏览器商店页面等受限页面运行。
- 截图只覆盖当前可见视口，不是整页长截图。
- 当前版本不做匿名审阅链接，也不使用共享项目 token。

## 常见问题

- Project 列表为空：重新登录后点击 `Refresh`。
- 批注模式打不开：在扩展页 reload 插件，并刷新目标网页。
- issue 创建了但看不到附件：刷新 issue 页面。
- 仍然看到旧行为：确认扩展页显示的版本号，并删除旧扩展后重新加载新目录。
