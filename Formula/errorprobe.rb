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
      sha256 "0ec5272f742ad43f0a7d2269108b668bd20cb8502b22dcb88bf4eaa9104d1ffb"
    end
  end

  def install
    bin.install "errorprobe-darwin-arm64" => "errorprobe"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/errorprobe --version")
  end
end
