#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="${VERSION:-v1.1.0}"
local_image="${LOCAL_IMAGE:-ftc-platform:${version}}"
go_binary="${GO:-go}"
kubectl_version="${KUBECTL_VERSION:-v1.32.13}"
output_dir="${repo_root}/dist"
work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

ca_bundle="${SSL_CERT_FILE:-}"
if [[ -z "${ca_bundle}" ]]; then
  for candidate in /etc/ssl/cert.pem /etc/ssl/certs/ca-certificates.crt; do
    if [[ -f "${candidate}" ]]; then
      ca_bundle="${candidate}"
      break
    fi
  done
fi
if [[ -z "${ca_bundle}" || ! -f "${ca_bundle}" ]]; then
  echo "cannot locate a CA certificate bundle; set SSL_CERT_FILE" >&2
  exit 1
fi

mkdir -p "${output_dir}"
cd "${repo_root}/web"
npm run build
cd "${repo_root}"

for arch in amd64 arm64; do
  rootfs="${work_dir}/${arch}"
  mkdir -p "${rootfs}/app/data" "${rootfs}/app/web" "${rootfs}/etc/ssl/certs" "${rootfs}/tmp" "${rootfs}/usr/local/bin"
  CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" "${go_binary}" build -trimpath -ldflags="-s -w -X main.platformVersion=${version}" -o "${rootfs}/app/ftc-platform" ./cmd/platform
  cp -R web/dist/. "${rootfs}/app/web/"
  cp "${ca_bundle}" "${rootfs}/etc/ssl/certs/ca-certificates.crt"

  if [[ "${arch}" == "amd64" ]]; then
    local_kubectl="${KUBECTL_AMD64:-}"
  else
    local_kubectl="${KUBECTL_ARM64:-}"
  fi
  if [[ -n "${local_kubectl}" ]]; then
    if [[ ! -f "${local_kubectl}" ]]; then
      echo "local kubectl for ${arch} does not exist: ${local_kubectl}" >&2
      exit 1
    fi
    cp "${local_kubectl}" "${rootfs}/usr/local/bin/kubectl"
  else
    kubectl_url="https://dl.k8s.io/release/${kubectl_version}/bin/linux/${arch}/kubectl"
    curl --fail --location --retry 3 --output "${rootfs}/usr/local/bin/kubectl" "${kubectl_url}"
    curl --fail --location --retry 3 --output "${rootfs}/kubectl.sha256" "${kubectl_url}.sha256"
    expected="$(tr -d '[:space:]' < "${rootfs}/kubectl.sha256")"
    actual="$(shasum -a 256 "${rootfs}/usr/local/bin/kubectl" | awk '{print $1}')"
    if [[ "${expected}" != "${actual}" ]]; then
      echo "kubectl ${arch} checksum mismatch" >&2
      exit 1
    fi
  fi
  chmod 0755 "${rootfs}/app/ftc-platform" "${rootfs}/usr/local/bin/kubectl"

  cat > "${rootfs}/Dockerfile" <<'DOCKERFILE'
FROM scratch
COPY --chown=10001:10001 app /app
COPY --chmod=0755 usr/local/bin/kubectl /usr/local/bin/kubectl
COPY etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --chown=10001:10001 tmp /tmp
USER 10001:10001
ENV LISTEN_ADDR=:8080 WEB_DIST_DIR=/app/web DATABASE_PATH=/app/data/platform.db BOOTSTRAP_TOKEN_PATH=/app/data/bootstrap-token SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
EXPOSE 8080 8081 8443
ENTRYPOINT ["/app/ftc-platform"]
DOCKERFILE

  docker buildx build --platform "linux/${arch}" --file "${rootfs}/Dockerfile" --tag "${local_image}-${arch}" --load "${rootfs}"
  docker image save --output "${output_dir}/ftc-platform-${version}-${arch}.tar" "${local_image}-${arch}"
done

cd "${output_dir}"
shasum -a 256 "ftc-platform-${version}-amd64.tar" "ftc-platform-${version}-arm64.tar" > SHA256SUMS
echo "Images and checksums written to ${output_dir}"
