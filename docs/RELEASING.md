# 最新提交的多平台构建与手动发布

源码与构建以当前分支的最新提交（`HEAD`）为准，不预设版本标签或提交SHA。已有标签仅标识对应的历史发布；历史[v0.1.0说明](releases/v0.1.0.md)不作为当前开发基准。

## 构建前提

- Go 1.25或更高版本、Git及go.mod锁定的依赖；打包需要tar、zip和shasum。
- 在仓库根目录运行，以下命令面向macOS/Linux的Bash，产物写入新临时目录；统一`CGO_ENABLED=0`，无需目标平台的C编译器。
- 如需取得远端更新，在干净的main分支先运行`git pull --ff-only`；下方脚本使用执行时本地当前分支的最新提交。
- 每次运行读取`HEAD`，导出该提交源码再编译，避免构建期间工作区变化影响产物；未提交修改不计入提交版本。打印完整SHA，文件名使用至少12位短SHA。
- 当前Schema采集使用Go MySQL驱动，构建前须取得锁定模块（可预先缓存）；CLI运行无需Python、Node或mysql客户端。验证脚本所需工具另见对应章节。

## 六个平台的编译命令

可以将整个代码块保存为shell脚本后使用Bash运行。最后会打印产物目录。

```bash
set -euo pipefail
BUILD_COMMIT=$(git rev-parse HEAD)
BUILD_SHORT=$(git rev-parse --short=12 "$BUILD_COMMIT")
RELEASE_VERSION="commit-${BUILD_SHORT}"
RELEASE_DIR=$(mktemp -d "/tmp/innodb-reader-${RELEASE_VERSION}.XXXXXX")
RELEASE_SOURCE="$RELEASE_DIR/source"
mkdir "$RELEASE_SOURCE"
git archive --format=tar "$BUILD_COMMIT" | tar -xf - -C "$RELEASE_SOURCE"

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
printf 'Source commit: %s\nRelease files: %s\n' "$BUILD_COMMIT" "$RELEASE_DIR"
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

Windows使用 `.\innodb-reader.exe --help`。固定仓库样本的完整命令见[CLI指南](CLI.md)。当前没有`--version`参数；以构建时打印的完整源码SHA标识版本，归档文件名含短SHA，SHA256SUMS用于校验产物。

在归档与SHA256SUMS所在目录验证下载完整性：

```sh
shasum -a 256 -c SHA256SUMS
```

Windows PowerShell使用`Get-FileHash -Algorithm SHA256`检查对应zip，将Hash与SHA256SUMS比较；文件名中的commit短SHA取本次构建结果。

发布说明应分别记录本次编译与实际运行检查；交叉编译成功不等于目标系统运行认证。回归方法见[第84章](format/84-release-validation.md)，既有结果不能代替对新提交的实际验证。

## 手动创建GitHub Release

日常开发和构建以最新提交为准，不要求每次提交创建版本标签。若用户需要GitHub Release：

1. 以本次实际构建的提交为发布对象，使用能标识该次发布的标签；该标签只记录发布，不作为后续开发基准。
2. 打开[新建Release页面](https://github.com/kimihiro-dev/innodb-go-reader/releases/new)，选择对应提交的发布标签并填写标题。
3. 根据本次提交的实际能力和验证填写说明，不直接套用旧版说明；可参考[当前支持矩阵](format/83-release-support-matrix.md)及[CLI指南](CLI.md)。
4. 上传四个tar.gz、两个zip及配套SHA256SUMS。核对标签的提交SHA、构建打印的SHA和归档文件名，确保它们来自同一源码。
5. 检查说明和资产后，由用户选择保存草稿或发布。

GitHub Release由用户手动创建；当前源码不会因为已有历史标签而停止更新，也不额外承诺永久Go API兼容性或扩大支持矩阵。
