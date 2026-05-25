class Errorprobe < Formula
  desc "Real-time error detection for Docker containers and Kubernetes pods"
  homepage "https://github.com/Veverke/ErrorProbe"
  version "0.0.0"
  url "https://github.com/Veverke/ErrorProbe/releases/download/v0.0.0/errorprobe-darwin-arm64"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  license "MIT"

  on_macos do
    on_arm do
      # version, url, and sha256 above are updated automatically by the
      # release workflow on every tag push.
    end
  end

  def install
    bin.install "errorprobe-darwin-arm64" => "errorprobe"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/errorprobe --version")
  end
end
