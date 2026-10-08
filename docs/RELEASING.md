# v0.1.0多平台构建与手动发布

源码标签：[`v0.1.0`](https://github.com/kimihiro-dev/innodb-go-reader/tree/v0.1.0)，固定提交 `116de5046aa94b379ce4d2e0711e34be9766c397`。发布说明见 [v0.1.0.md](releases/v0.1.0.md)，可直接复制到GitHub Release描述中。

## 构建前提

- Go 1.25或更高版本、Git；打包还需要tar、zip和shasum。
- 在本项目Git仓库根目录运行。以下命令面向macOS/Linux的Bash，产物写入新临时目录，不修改当前分支或工作区，不需要Windows交叉编译器。
- 使用 `git archive v0.1.0` 解出固定源码后再构建。即使main后来更新，也不会把新代码误打包为v0.1.0。
- 当前产品只使用Go标准库，统一 `CGO_ENABLED=0`，每个目标通过GOOS/GOARCH选择；不需要Python、Node、MySQL服务或第三方Go依赖来编译CLI。

## 六个平台的编译命令

可以将整个代码块保存为shell脚本后使用Bash运行。最后会打印产物目录。

```bash
set -euo pipefail
RELEASE_VERSION=v0.1.0
RELEASE_DIR=$(mktemp -d "/tmp/innodb-reader-${RELEASE_VERSION}.XXXXXX")
RELEASE_SOURCE="$RELEASE_DIR/source"
mkdir "$RELEASE_SOURCE"
git archive --format=tar "$RELEASE_VERSION" | tar -xf - -C "$RELEASE_SOURCE"

(
  cd "$RELEASE_SOURCE"
  export CGO_ENABLED=0

  # macOS Intel / Apple Silicon
  GOOS=darwin GOARCH=amd64 go build -trimpath \
    -o "$RELEASE_DIR/innodb-reader_${RELEASE_VERSION}_darwin_amd64/innodb-reader" ./cmd/innodb-reader
  GOOS=darwin GOARCH=arm64 go build -trimpath \
    -o "$RELEASE_DIR/innodb-reader_${RELEASE_VERSION}_darwin_arm64/innodb-reader" ./cmd/innodb-reader

  # Linux x86-64 / ARM64
  GOOS=linux GOARCH=amd64 go build -trimpath \
    -o "$RELEASE_DIR/innodb-reader_${RELEASE_VERSION}_linux_amd64/innodb-reader" ./cmd/innodb-reader
  GOOS=linux GOARCH=arm64 go build -trimpath \
    -o "$RELEASE_DIR/innodb-reader_${RELEASE_VERSION}_linux_arm64/innodb-reader" ./cmd/innodb-reader

  # Windows x86-64 / ARM64
  GOOS=windows GOARCH=amd64 go build -trimpath \
    -o "$RELEASE_DIR/innodb-reader_${RELEASE_VERSION}_windows_amd64/innodb-reader.exe" ./cmd/innodb-reader
  GOOS=windows GOARCH=arm64 go build -trimpath \
    -o "$RELEASE_DIR/innodb-reader_${RELEASE_VERSION}_windows_arm64/innodb-reader.exe" ./cmd/innodb-reader
)

# macOS / Linux：tar.gz，保留可执行权限，排除macOS扩展属性副文件
for RELEASE_PLATFORM in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
  RELEASE_PACKAGE="innodb-reader_${RELEASE_VERSION}_${RELEASE_PLATFORM}"
  COPYFILE_DISABLE=1 tar -czf "$RELEASE_DIR/$RELEASE_PACKAGE.tar.gz" -C "$RELEASE_DIR" "$RELEASE_PACKAGE"
done

# Windows：zip
for RELEASE_PLATFORM in windows_amd64 windows_arm64; do
  RELEASE_PACKAGE="innodb-reader_${RELEASE_VERSION}_${RELEASE_PLATFORM}"
  (
    cd "$RELEASE_DIR"
    zip -q -r "$RELEASE_PACKAGE.zip" "$RELEASE_PACKAGE"
  )
done

# 文件名相对于下载目录，方便用户校验
(
  cd "$RELEASE_DIR"
  shasum -a 256 innodb-reader_${RELEASE_VERSION}_*.tar.gz \
    innodb-reader_${RELEASE_VERSION}_*.zip > SHA256SUMS
)
printf 'Release files: %s\n' "$RELEASE_DIR"
```

| 文件名后缀 | 平台 | 包内程序 |
|---|---|---|
| `darwin_amd64.tar.gz` | macOS Intel | `innodb-reader` |
| `darwin_arm64.tar.gz` | macOS Apple Silicon | `innodb-reader` |
| `linux_amd64.tar.gz` | Linux x86-64 | `innodb-reader` |
| `linux_arm64.tar.gz` | Linux ARM64 | `innodb-reader` |
| `windows_amd64.zip` | Windows x86-64 | `innodb-reader.exe` |
| `windows_arm64.zip` | Windows ARM64 | `innodb-reader.exe` |

每个包只有对应平台的程序；使用文档在仓库中。原始二进制目录和source目录用于本地核对，不作为额外Release资产上传。

## 验证与证据范围

解压匹配本机的包后，可以运行：

```sh
./innodb-reader --help
./innodb-reader metadata /path/to/stable-table.ibd
./innodb-reader check --scope rows /path/to/stable-table.ibd
```

Windows使用 `.\innodb-reader.exe --help`。固定仓库样本的完整命令见[CLI指南](CLI.md)。当前没有 `--version` 参数，版本身份通过源码标签、归档文件名和SHA256SUMS确定。

在归档与SHA256SUMS所在目录验证下载完整性：

```sh
shasum -a 256 -c SHA256SUMS
```

Windows PowerShell可用 `Get-FileHash .\innodb-reader_v0.1.0_windows_amd64.zip -Algorithm SHA256`，将Hash与SHA256SUMS中对应文件的值比较；ARM64包替换文件名即可。

发布说明必须区分交叉编译与运行验证：macOS ARM64为当前本机运行环境；其他五个平台通过交叉编译，但本轮不声称已完成目标系统运行测试。既有完整回归证据见[第84章](format/84-release-validation.md)，它不等于本轮重新执行全部长回归。

## 手动创建GitHub Release

1. 打开[新建Release页面](https://github.com/kimihiro-dev/innodb-go-reader/releases/new)。
2. 选择已存在的标签 `v0.1.0`，标题填写 `InnoDB Go Reader v0.1.0`。不要另建一个指向最新main的同名标签。
3. 将 [v0.1.0.md](releases/v0.1.0.md) 内容复制到描述框。
4. 上传四个tar.gz、两个zip和SHA256SUMS，共七个文件。构建机器或工具链不同会得到不同校验值，上传与本次实际资产配套的校验文件。
5. 检查说明与资产后，由用户选择保存草稿或发布Release。

本轮只创建源码标签和发布准备文档，不自动创建GitHub Release。v0.1.0标识这次固定源码，不额外承诺稳定Go API或扩大MySQL/格式支持矩阵。已发布标签保持不变，后续修订使用新版本号。
