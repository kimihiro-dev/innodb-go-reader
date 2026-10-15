# 基础库验证与交付

## 当前交付内容

本仓库交付`github.com/kimihiro-dev/innodb-go-reader`及可选rowio/stats包、库示例和物理格式资料。命令行二进制的跨平台构建和手动发布由[CLI项目](https://github.com/kimihiro-dev/innodb-go-reader-cli/blob/main/docs/RELEASING.md)维护。

开发和构建跟随当前分支最新提交，已有标签仅表示历史版本。当前工作树修改须先保存为实际提交，再用于远程依赖和发布；不将旧标签或旧提交冒充新模块边界。

## 库验证

在本仓库根目录运行，需要Go 1.25或更高版本；本库没有第三方Go模块依赖：

```sh
GOWORK=off go build -mod=readonly ./...
GOWORK=off go test -race -timeout=25m -mod=readonly ./...
GOWORK=off go vet ./...
python3 scripts/check_docs.py
```

完整性能/主动fuzz验证可以使用`python3 scripts/verify_release.py --out /tmp/new-library-verification`；目录须不存在，脚本检查当前库、rowio/stats及示例，不再构建CLI。固定首版证据见[第84章](format/84-release-validation.md)，历史通过不替代本次检查。

## 版本与源码追溯

每次交付记录实际`git rev-parse HEAD`及执行过的检查。源码归档可以用`git archive HEAD`，未提交文件不会进入归档。Go库通过模块版本或伪版本消费，不生成innodb-reader二进制，也不要求每个开发提交创建标签。

CLI更新到已保存并可远程取得的库提交后，提交对应go.mod/go.sum，并在禁用工作区的环境验证。CLI通过go get选择已推送的库提交时，Go会生成对应伪版本；无需为了每个开发提交创建标签。GitHub远程仓库操作、推送、标签和Release发布仍按用户授权执行。
