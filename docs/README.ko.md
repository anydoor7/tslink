<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>컴퓨터나 서버의 앱마다 내 Tailscale 네트워크 안의 비공개 주소를 주고, 누가 접속할 수 있는지는 직접 정하세요.</strong></p>

웹 앱, 폴더, 모델 API, 데이터베이스를 내 휴대폰과 노트북에서 열 수 있고, 앱마다 상태 점검과 접속 이력이 따라옵니다. AI 에이전트도 내가 준 역할 범위 안에서, 자신이 localhost에 띄운 앱에 다른 기기에서 열 수 있는 비공개 주소를 붙이고 그 앱을 확인할 수 있습니다. 다른 사람이 써야 할 때는 지정한 사람에게 날짜를 정해 접근을 허용하거나, 웹 앱 하나를 정해진 시간 동안 공개 인터넷에 열 수 있습니다.

**Tailscale이 필요합니다.** Tailscale 계정(개인 사용은 무료)이 있어야 하고, 비공개 앱을 여는 기기마다 Tailscale 앱이 필요합니다. 게스트와 공개 방문자는 브라우저만 있으면 됩니다. TSLink는 독립 프로젝트이며, Tailscale이 만들거나 보증한 것이 아닙니다. [요구 사항](#requirements)

<p align="center"><a href="#quickstart">빠른 시작</a> · <a href="#agents">에이전트 안내</a> · <a href="comparison.md">Serve, ngrok, Cloudflare와 비교</a> · <a href="#documentation">문서</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <strong>한국어</strong> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## 할 수 있는 일

### 내 앱에 접속하기

- **앱마다 주소 하나.** `tslink share 3000`, `tslink share ./photos`, `tslink add db --tcp localhost:5432`를 실행하면 웹 앱, 폴더, 파일, TCP 서비스가 tailnet(나만의 비공개 Tailscale 네트워크) 안에서 `https://photos.<tailnet>.ts.net` 같은 자기만의 비공개 주소를 갖습니다. 앱마다 별도의 Tailscale 기기가 되므로 IP 주소와 포트 대신 이름으로 앱을 엽니다.
- **따로 정하지 않으면 비공개.** TSLink는 기본적으로 앱 접근을 비공개로 유지하고, 어떤 기기가 연결할 수 있는지는 tailnet 정책이 정합니다. 공개 앱 엔드포인트는 게스트 링크를 만들거나 Funnel로 명시적으로 게시할 때만 열립니다.
- **한곳에서 모아 보기.** `tslink status --urls`는 이 컴퓨터에 등록된 앱을 보여 주고, 선택 사항인 비공개 홈 페이지에서는 그 앱들의 주소와 상태를 볼 수 있습니다. [포털](portal.md)
- **문제가 생기면 바로 알기.** 앱이 멈추거나 다시 살아날 때, 또는 Tailscale 로그인이 곧 만료될 때 백그라운드 상태 점검이 명령이나 웹훅으로 알려 줍니다. 접속 이력에는 누가 언제 어떤 앱을 열었는지가 거부된 요청까지 남습니다. [상태 점검과 알림](health-and-alerts.md) · [접속 이력](access-log.md)
- **앱 레시피.** Home Assistant, Jellyfin, Immich, Ollama를 포함한 셀프호스팅 앱 15개의 레시피가 있고, `tslink apps detect`는 이미 로컬에서 수신 대기 중인 지원 앱을 찾을 수 있습니다. 큰 사진과 동영상을 올리려면 [앱별 업로드 제한을 높이세요](sharing.md). [앱 구성 레시피](apps.md) · [로컬 AI](local-ai.md)

### 에이전트에게 맡기기

에이전트가 개발 서버, 미리보기, 로컬 모델 API를 `localhost`에 띄우면 휴대폰이나 다른 컴퓨터에서는 그 주소에 접속할 수 없습니다. TSLink를 쓰면 에이전트가 내가 정한 한도 안에서 여기에 비공개 주소를 붙이고, 정확한 URL을 알려 주고, 등록을 다시 해제할 수 있습니다.

- **공유, 확인, 되돌리기.** `share --json`은 등록한 이름과 함께 정확한 URL 또는 내가 열어야 할 로그인 링크를 돌려줍니다. `url <name> --wait`와 `status --urls --name <name>`은 엔드포인트가 준비됐는지 알려 주고, `remove <name>`(MCP에서는 `unshare`)은 공유를 제거합니다. [에이전트 빠른 시작](agent-quickstart.md)
- **자동화에 맞춘 설계.** `tslink mcp`를 제외한 관리 명령은 `--json`을 받아 버전이 붙은 결과와 안정적인 오류 코드를 돌려줍니다. `tslink mcp`는 JSON-RPC로 로컬 MCP 클라이언트에 앱과 접근 관련 도구를 제공합니다. 호출자 바인딩을 설정하면 `tslink serve --mcp`가 tailnet을 통해 다른 기기의 MCP 클라이언트에 같은 도구를 제공합니다. [JSON 자동화](json-automation.md) · [원격 MCP](remote-mcp.md)
- **제한된 권한.** 로컬 에이전트는 기본적으로 소유자 권한을 가집니다. 에이전트에 낮은 역할(`viewer`, `app-operator`, `people-manager`)을 줄 수 있습니다. 이 역할은 지정한 앱에만 적용되고, 에이전트가 부여하는 접근의 최대 기간도 제한합니다. MCP로 한 변경은 기록되며 `tslink mcp-audit`로 볼 수 있습니다. 역할은 TSLink의 도구만 제한할 뿐, 에이전트 자체의 셸이나 파일까지 막지는 않습니다. [MCP 권한](mcp-scopes.md)

### 정한 사람과 공유하기

- **지정한 사람에게, 정한 날짜까지.** `tslink people add alice@example.com --apps photos,notes --for 7d`를 실행하면 해당 Tailscale 로그인이 기한까지 그 웹 앱과 파일 앱을 열 수 있습니다. `people update`, `extend`, `people remove`로 바꾸거나 끝낼 수 있습니다. 삭제하면 그 사람의 다음 요청부터 거부되지만, 이미 내려받은 것은 되돌릴 수 없습니다. [사용자 공유](people.md) · [유효 기간](durations.md)
- **tailnet 밖에 있는 사람.** `--invite --print-links`를 붙이면 앱마다 기기 초대가 담긴 메시지 하나가 바로 보낼 수 있는 형태로 만들어집니다(사용자 소유 API 토큰 필요). `--qr`은 휴대폰 설정용 코드를 출력합니다.
- **접근 요청.** tailnet 안의 사람은 홈 페이지에서 기간 연장이나, 요청 가능으로 표시한 앱의 접근을 요청할 수 있습니다. 기간을 정해 명령 하나로 승인합니다. [접근 요청](requests.md)

### 웹 앱을 잠시 인터넷에 열기

- **게스트 링크.** `tslink guest create photos --for 3d --public --print-link`는 웹 앱 하나에 대한 브라우저 링크를 만듭니다. PIN은 선택이며 링크마다 따로 취소할 수 있습니다. 게스트에게는 Tailscale 계정이 필요 없습니다. 링크를 가진 사람은 누구나 쓸 수 있으므로 누가 방문했는지 증명하지는 못합니다. [게스트 링크](guest-links.md)
- **공개 URL.** `tslink add preview --proxy localhost:3000 --funnel --public`은 URL을 아는 누구에게나 웹 앱을 공개합니다. 새로 게시하면 기본으로 24시간 동안 유지되며, `--funnel-ttl`로 다른 기간을 정할 수 있습니다. [Funnel](funnel.md)
- 새 게스트 링크와 새 공개 게시는 Tailscale Funnel을 거치고 기간이 정해져 있습니다(최소 1시간, 기본 최대 7일, 소유자가 변경 가능). 이 공개 경로는 HTTP 프록시 앱을 지원하며, 폴더나 파일을 직접 제공하는 서비스와 원시 TCP는 비공개로 남습니다.

위의 기능은 모두 v0.1.0에 포함되어 있습니다.

<a id="requirements"></a>

## 요구 사항

TSLink는 Tailscale을 기반으로 만들어졌습니다. 독립 프로젝트로서 Tailscale이 만들거나 보증한 것이 아니며, Tailscale의 약관과 [요금제](https://tailscale.com/pricing)가 그대로 적용됩니다.

| 대상 | 필요한 것 |
|---|---|
| 나 | [MagicDNS와 HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates)를 켠 Tailscale 계정. 무료 Personal 요금제는 비상업적 용도로만 쓸 수 있습니다. |
| 앱을 실행하는 컴퓨터나 서버 | TSLink만 있으면 됩니다. Tailscale이 내장되어 있어 Tailscale을 따로 설치하지 않아도 됩니다. 기본 설정에서는 새 앱 노드마다 브라우저 로그인이 필요하고 기기 승인이 필요할 수도 있습니다. [저장된 자격 증명](credentials-and-tags.md)을 쓰면 앱마다 브라우저로 로그인하지 않고 등록할 수 있습니다. |
| 내 다른 기기 | tailnet에 로그인한 Tailscale 앱. |
| 내가 정한 사람 | Tailscale 앱과 본인 로그인. 내 tailnet에 참여하거나(요금제의 사용자가 한 명 늘어납니다), 앱마다 기기 초대를 수락합니다. tailnet 정책에서 이들의 접근을 허용해야 합니다. |
| 게스트와 공개 방문자 | 브라우저. tailnet에서 Funnel을 허용해야 하며, Tailscale은 Funnel을 아직 베타로 분류합니다. |

앱용 HTTPS 인증서가 발급되면 그 앱의 Tailscale 기기 이름과 tailnet DNS 이름이 공개 인증서 로그에 올라갑니다. 남이 봐도 괜찮은 앱 이름을 고르세요.

<a id="installation"></a>
<a id="quickstart"></a>

## 빠른 시작

macOS와 Linux에서는 Homebrew로 설치합니다. macOS 바이너리는 Developer ID 인증서로 서명되고 Apple의 공증을 받았습니다. 나중에 업그레이드하려면 `brew upgrade --cask tslink`를 실행하고, TSLink를 백그라운드 서비스로 실행 중이라면 `tslink install`을 다시 실행하세요.

```bash
brew install --cask anydoor7/tap/tslink
```

Windows에서는 [최신 릴리스](https://github.com/anydoor7/tslink/releases/latest)에서 `tslink_<version>_windows_<arch>.zip`을 내려받아 `checksums.txt`로 확인한 뒤 `tslink install`을 실행하면 로그인할 때 TSLink가 시작됩니다. zip 파일에는 Authenticode 서명이 없으므로 서명된 체크섬과 빌드 출처 증명으로 [릴리스를 검증](verify-release.md)하세요. Linux용 `.deb`, `.rpm` 패키지도 같은 릴리스 페이지에 있습니다. 소스에서 빌드하려면 **Git과 Go 1.26.6+** 환경이 필요합니다. 아래 명령은 bash/zsh용입니다. [macOS, Linux, Windows 설정](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

**Tailscale 계정**과 [MagicDNS와 HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates)가 필요합니다. 비공개 접속 기기는 Tailscale과 네트워크 정책의 허용이 필요합니다. 앱 호스트에는 TSLink가 Tailscale을 내장합니다.

앱이 이미 3000 포트에서 실행 중이라면:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

사용하지 않는 이름을 지정하세요. `share`가 다른 이름을 반환하면 `url`에도 그 이름을 사용하세요. 표시된 브라우저 등록과 기기 승인을 먼저 완료하고, 허용된 기기에서 정확한 앱 URL을 여세요. `share`는 필요할 때 백그라운드 서비스를 시작합니다. 첫 비공개 사용에는 관리자 API 토큰이 필요 없습니다. 파일은 `tslink share ./report.html`로 공유합니다. 앱은 실행 중이어야 하고 파일은 존재해야 합니다. [전체 설정](getting-started.md)

잘 사용하고 있다면 [TSLink에 Star](https://github.com/anydoor7/tslink)를 남겨 다른 사람이 발견하도록 도와주세요. 전적으로 선택 사항입니다.

<a id="architecture"></a>

## 동작 방식

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="PC 또는 클라우드 호스트 하나에서 CLI/MCP가 공용 데몬과 앱별 노드를 관리합니다. 비공개 기기는 Tailscale 암호화 경로로, 선택적 공개 HTTPS/Funnel은 게스트 인증 또는 명시적 공개 설정을 통해 HTTP 앱에 연결됩니다." width="960">
</picture>

앱으로 이어지는 암호화된 사설 경로라고 생각하세요. **Tailscale은 네트워크 전송과 HTTPS를, TSLink는 호스트별 앱 접근 관리를 담당합니다.** 하나의 데몬이 서비스마다 별도의 내장 노드를 실행합니다. 비공개 포털에는 허용된 앱을 표시하며 상태와 접속 이력으로 유지 관리를 돕습니다.

공개 접속은 직접 켜야 합니다. 게스트는 링크와 선택적 PIN이 필요하며, 개방형 Funnel은 URL을 가진 누구나 접속할 수 있습니다. 두 방식 모두 공개 HTTPS를 사용하며 비공개 사용자 신원 인증과 다릅니다. 원시 TCP는 비공개로 유지되고 tailnet 정책과 백엔드 인증에 의존합니다. TSLink는 앱 설치, 호스트 프로세스 격리, 클라우드 VPC 생성, 다중 호스트 통합을 하지 않습니다. Tailscale과 함께 쓰는 독립 프로젝트입니다. [구조와 경계](architecture.md)

<a id="agents"></a>

## 에이전트 안내

CLI/MCP로 목록, 상태, URL, 접근 권한을 관리하세요. [에이전트 빠른 시작](agent-quickstart.md)을 읽고 실제 도구 스키마를 확인한 뒤 앱 접속을 검증해야 성공으로 보고할 수 있습니다.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

CLI 자동화는 `--json`, MCP는 stdio 기반 JSON-RPC를 사용합니다. [클라이언트 설정](mcp-clients.md) · [원격 MCP](remote-mcp.md) · [역할과 앱 범위](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## 문서와 라이선스

[전체 가이드](INDEX.md) · [CLI 참조](cli-reference.md) · [로컬 AI](local-ai.md) · [상태 점검](health-and-alerts.md) · [접속 이력](access-log.md) · [로드맵](roadmap.md)

다중 호스트 목록은 계획 중입니다. [기여](../CONTRIBUTING.md)와 [보안 제보](../SECURITY.md)를 환영합니다. [Apache 2.0](../LICENSE)은 상업적 사용을 허용합니다. 재배포 시 [NOTICE](../NOTICE)와 [타사 고지](../THIRD_PARTY_NOTICES.md)를 유지하세요. [상업적 협력](../COMMERCIAL.md)은 자발적입니다. Tailscale 약관과 요금제는 별도로 적용됩니다.
