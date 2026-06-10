class Errorprobe < Formula
  desc "Real-time error detection for Docker containers and Kubernetes pods"
  homepage "https://github.com/Veverke/ErrorProbe"
  version "1.0.0"
  license "MIT"

  on_macos do
    on_arm do
      # version, url, and sha256 are updated automatically by the
      # release workflow on every tag push.
      url "https://github.com/Veverke/ErrorProbe/releases/download/v1.0.0/errorprobe-darwin-arm64"
      sha256 "fb09bd24fd525af164a44e06033f6a0fd93b4a2af86ed751e9c34e41af8989aa"
    end
  end

  def install
    bin.install "errorprobe-darwin-arm64" => "errorprobe"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/errorprobe --version")
  end
end
