### Installation Guide
build locally using `go build -ldflags="-X 'main.version=$(git describe --tags --always --dirty)'" -o ippon .`
install using `go install -ldflags="-X 'main.version=$(git describe --tags --always --dirty)'" github.com/your-repo/ippon@vx.x.x`