# impl/34 — E34 공급망·릴리즈 (FR-R03, R05, R07, S08, X06, NFR-S04)

| # | 수용 기준 | 방법 |
|---|---|---|
| 1 | goreleaser: CLI·Controller·**Agent** 3 바이너리 × 6 플랫폼, 재현 빌드 플래그(`-trimpath`, `-buildid=`, commit 시각 mtime, CGO 0), 아카이브·deb/rpm·ghcr 이미지(nonroot distroless) | `scripts/release-lint.py`, `scripts/repro-check.sh` |
| 2 | 서명: macOS Developer ID + notarytool(비밀 있을 때만), Windows Authenticode(Azure Trusted Signing/jsign), checksums cosign keyless + GPG(Linux 저장소), 이미지 cosign — 모든 키는 CI 비밀(`release` environment), 저장소에 키 없음 | release-lint (비밀은 `${{ secrets.* }}` 로만, run 스크립트 보간 금지, 키 파일·PEM 개인키 탐지) |
| 3 | SBOM(syft, 아카이브·패키지·이미지) + SLSA 빌드 출처 증명(`attest-build-provenance`), 검증 방법을 릴리즈 노트에 | release-lint |
| 4 | CI 매트릭스 FR-R05(ubuntu-22.04·macos-14·windows-2022) + 공급망 job(govulncheck·라이선스 인벤토리·재현 빌드·release-lint·pnpm audit) + dependabot(go·npm·cargo·actions·docker) | release-lint |
| 5 | 라이선스 고지: 바이너리에 링크되는 모듈만 THIRD_PARTY_NOTICES.md 로 생성, 라이선스 불명 모듈이 있으면 실패 | `go run ./tools/notices` |
| 6 | 선택 의존성 안내(macFUSE·WinFsp·FUSE-T·Samba·mergerfs·smartmontools, 라이선스·설치), 아카이브에 포함 | `docs/optional-dependencies.md` |
| 7 | 확장 스토어 배포: Chrome Web Store(공개/trusted testers)·Edge Add-ons(`wxt submit`, 자격 없으면 생략), 기업 강제 설치 정책(Linux JSON·Windows .reg Chrome/Edge·macOS mobileconfig·자체 호스팅 updates.xml) | release-lint (JSON·plist·XML·REG 검사), `wxt zip` |
| 8 | 데스크톱 번들 서명·공증(Tauri, macOS universal / Windows / Linux) | release-lint |
| 9 | 검사기 자체 검증: 비밀 보간·플래그 누락·잘못된 build id·개인키 커밋을 실제로 잡아냄 | 변이 시험 |
