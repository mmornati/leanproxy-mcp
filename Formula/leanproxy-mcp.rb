class LeanproxyMcp < Formula
  desc "LeanProxy-MCP - A lightweight token firewall for MCP servers"
  homepage "https://github.com/mmornati/leanproxy-mcp"
  license "MIT"

  version "0.9.2"

  on_macos do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.9.2/leanproxy-mcp_0.9.2_darwin_arm64.tar.gz"
      sha256 "1c1df5212a18d378abe3d66a0d5fcdb82b88063965462664c3341bc1e00d3be3"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.9.2/leanproxy-mcp_0.9.2_darwin_amd64.tar.gz"
      sha256 "b76f76ef68fb7feef52425664ac2fa6d1a0de536d632b9e588316fcb092a3939"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.9.2/leanproxy-mcp_0.9.2_linux_arm64.tar.gz"
      sha256 "e3436bd2224dea09db27683c809203a1ef2893e60e81693aef8a9ad1cbf456b5"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.9.2/leanproxy-mcp_0.9.2_linux_amd64.tar.gz"
      sha256 "0f06771da3a406e64db105c7b38ceea3352412ad5c9ce54ef33353dbc4ca78a6"
    end
  end

  def install
    bin.install "leanproxy-mcp"
  end

  test do
    system "#{bin}/leanproxy-mcp", "version"
  end
end
