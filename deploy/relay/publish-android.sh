#!/usr/bin/env bash
# Build, sign and publish the Android phone and Wear OS APKs to the relay, so
# both apps can update themselves (docs/GUIDE.md § Android updates).
#
# Usage: deploy/relay/publish-android.sh [<ssh-target>]
#        make publish-android
#
# <ssh-target> comes from the argument, else AW_RELAY_SSH in agent-watch.env;
# SSH options come from SSH_OPTS, else AW_RELAY_SSH_OPTS. The SSH user must be
# root (it writes the relay's update directory and chowns files to agentwatch).
#
# Refuses to run from a dirty tree or with a debug-signed APK. Each app is
# published only when its version code increased; a lower code is refused. The
# release key comes from wearos-app/keystore.properties (git-ignored;
# docs/GUIDE.md § Android updates). The relay needs no restart: it reads the
# metadata on each request. The previous APKs stay for seven days.
set -euo pipefail

usage() {
	echo "usage: publish-android.sh [<ssh-target>]   (optional env: SSH_OPTS=\"-i key -o Port=2222\", AW_ENV_FILE)" >&2
	exit "${1:-1}"
}

VPS=""
while [ $# -gt 0 ]; do
	case "$1" in
	-h | --help) usage 0 ;;
	-*) echo "publish-android.sh: unknown option $1" >&2 && usage ;;
	*)
		[ -z "$VPS" ] || usage
		VPS="$1"
		;;
	esac
	shift
done

repo="$(git rev-parse --show-toplevel)"
awenv="$repo/tools/config/awenv.sh"
env_file="${AW_ENV_FILE:-$repo/agent-watch.env}"
case "$env_file" in /*) ;; *) env_file="$PWD/$env_file" ;; esac

file_value() {
	local v
	v="$("$awenv" get "$env_file" "$1")"
	case "$v" in *\"* | *\'*)
		echo "publish-android.sh: $1 in $env_file is quoted; write the value bare" >&2
		exit 1
		;;
	esac
	printf '%s' "$v"
}

if [ -z "$VPS" ]; then
	if [ ! -f "$env_file" ]; then
		echo "publish-android.sh: no <ssh-target> given and no $env_file to read AW_RELAY_SSH from" >&2
		usage
	fi
	VPS="$(file_value AW_RELAY_SSH)"
	[ -n "$VPS" ] || {
		echo "publish-android.sh: no <ssh-target> given and AW_RELAY_SSH is empty in $env_file" >&2
		exit 1
	}
	if [ -z "${SSH_OPTS+set}" ]; then
		SSH_OPTS="$(file_value AW_RELAY_SSH_OPTS)"
	fi
fi
# shellcheck disable=SC2206 # word splitting of SSH_OPTS is intended
SSH_ARGS=(${SSH_OPTS:-})

cd "$repo"

if [ -n "$(git status --porcelain)" ]; then
	echo "publish-android.sh: refusing to publish a dirty tree (the APK would not match any commit):" >&2
	git status --short >&2
	exit 1
fi
if [ ! -f wearos-app/keystore.properties ]; then
	echo "publish-android.sh: wearos-app/keystore.properties is missing." >&2
	echo "Create the release key and the properties file first (docs/GUIDE.md § Android updates)." >&2
	exit 1
fi
if [ ! -f VERSIONS ]; then
	echo "publish-android.sh: $repo/VERSIONS not found" >&2
	exit 1
fi

version_of() {
	local v
	v="$(sed -n "s/^$1=//p" VERSIONS | tail -n 1)"
	[ -n "$v" ] || {
		echo "publish-android.sh: $1 is not set in VERSIONS" >&2
		exit 1
	}
	printf '%s' "$v"
}
version_code_of() {
	local v
	v="$(version_of "$1")"
	case "$v" in
	'' | *[!0-9]* | 0*)
		echo "publish-android.sh: $1 must be a positive integer without leading zeros (got $v)" >&2
		exit 1
		;;
	esac
	printf '%s' "$v"
}

phone_name="$(version_of MOBILE_VERSION_NAME)"
phone_code="$(version_code_of MOBILE_VERSION_CODE)"
watch_name="$(version_of WEAROS_VERSION_NAME)"
watch_code="$(version_code_of WEAROS_VERSION_CODE)"
for name in "$phone_name" "$watch_name"; do
	case "$name" in
	*[!A-Za-z0-9._+-]*)
		echo "publish-android.sh: version names may only use letters, digits, '.', '_', '+' and '-' (got $name)" >&2
		exit 1
		;;
	esac
done

sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"
if [ -z "$sdk" ] && [ -f wearos-app/local.properties ]; then
	sdk="$(sed -n 's/^[[:space:]]*sdk\.dir[[:space:]]*=[[:space:]]*//p' wearos-app/local.properties | tail -n 1)"
fi
if [ -z "$sdk" ] || [ ! -d "$sdk" ]; then
	sdk="$HOME/Library/Android/sdk"
fi
apksigner="$(find "$sdk/build-tools" -type f -name apksigner 2>/dev/null | sort -V | tail -n 1)"
[ -n "$apksigner" ] || {
	echo "publish-android.sh: apksigner not found under $sdk/build-tools (set ANDROID_HOME)" >&2
	exit 1
}

echo "building Android releases: phone $phone_name ($phone_code), watch $watch_name ($watch_code)"
(
	cd wearos-app
	./gradlew :mobile:assembleRelease :app:assembleRelease
)

phone_apk="wearos-app/mobile/build/outputs/apk/release/mobile-release.apk"
watch_apk="wearos-app/app/build/outputs/apk/release/app-release.apk"
for apk in "$phone_apk" "$watch_apk"; do
	[ -f "$apk" ] || {
		echo "publish-android.sh: expected $apk after the build" >&2
		exit 1
	}
	certs="$("$apksigner" verify --print-certs "$apk" 2>/dev/null)" || {
		echo "publish-android.sh: $apk is not signed" >&2
		exit 1
	}
	if grep -q 'CN=Android Debug' <<<"$certs"; then
		echo "publish-android.sh: $apk is debug-signed; refusing to publish it." >&2
		echo "Check wearos-app/keystore.properties and build again." >&2
		exit 1
	fi
done

local_tmp="$(mktemp -d)"
trap 'rm -rf "$local_tmp"' EXIT

phone_file="phone-$phone_code.apk"
watch_file="watch-$watch_code.apk"
cp "$phone_apk" "$local_tmp/$phone_file"
cp "$watch_apk" "$local_tmp/$watch_file"

sha_of() { shasum -a 256 "$1" | awk '{print $1}'; }
size_of() { wc -c <"$1" | tr -d '[:space:]'; }

phone_sha="$(sha_of "$local_tmp/$phone_file")"
phone_size="$(size_of "$local_tmp/$phone_file")"
watch_sha="$(sha_of "$local_tmp/$watch_file")"
watch_size="$(size_of "$local_tmp/$watch_file")"

cat >"$local_tmp/phone.json" <<EOF
{"app":"phone","version_code":$phone_code,"version_name":"$phone_name","sha256":"$phone_sha","size":$phone_size,"apk":"$phone_file"}
EOF
cat >"$local_tmp/watch.json" <<EOF
{"app":"watch","version_code":$watch_code,"version_name":"$watch_name","sha256":"$watch_sha","size":$watch_size,"apk":"$watch_file"}
EOF

remote_tmp="$(ssh ${SSH_ARGS[@]+"${SSH_ARGS[@]}"} "$VPS" 'mktemp -d /tmp/agent-watch-updates.XXXXXX')"
case "$remote_tmp" in
/tmp/agent-watch-updates.*) ;;
*)
	echo "publish-android.sh: unexpected remote temporary directory: $remote_tmp" >&2
	exit 1
	;;
esac
trap 'rm -rf "$local_tmp"; ssh ${SSH_ARGS[@]+"${SSH_ARGS[@]}"} "$VPS" "rm -rf $(printf %q "$remote_tmp")" >/dev/null 2>&1 || true' EXIT

scp -q ${SSH_ARGS[@]+"${SSH_ARGS[@]}"} \
	"$local_tmp/$phone_file" "$local_tmp/$watch_file" \
	"$local_tmp/phone.json" "$local_tmp/watch.json" "$VPS:$remote_tmp/"

# shellcheck disable=SC2087 # the remote script is quoted on purpose; its args follow bash -s
ssh ${SSH_ARGS[@]+"${SSH_ARGS[@]}"} "$VPS" bash -s -- \
	"$remote_tmp" "$phone_code" "$phone_file" "$watch_code" "$watch_file" <<'REMOTE'
set -euo pipefail
remote_tmp="$1"
phone_code="$2"
phone_file="$3"
watch_code="$4"
watch_file="$5"
env_file=/etc/agent-watch-relay/env
trap 'rm -rf "$remote_tmp"' EXIT

dir="$(sed -n 's/^[[:space:]]*AW_UPDATE_DIR[[:space:]]*=//p' "$env_file" 2>/dev/null | tail -n 1 | tr -d "\"' ")"
dir="${dir:-/var/lib/agent-watch-relay/updates}"
case "$dir" in
/*) ;;
*)
	echo "AW_UPDATE_DIR must be an absolute path (got $dir)" >&2
	exit 1
	;;
esac

installed_code() {
	local file="$dir/$1.json"
	[ -f "$file" ] || return 1
	sed -n 's/.*"version_code"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$file" | head -n 1
}

changed=""
for app in phone watch; do
	case "$app" in
	phone) new="$phone_code" ;;
	watch) new="$watch_code" ;;
	esac
	old="$(installed_code "$app" || true)"
	case "$old" in
	'' | *[!0-9]*) old="" ;;
	esac
	if [ -n "$old" ] && [ "$new" -lt "$old" ]; then
		echo "refusing to publish $app $new: the relay already has $old; VERSIONS must increase" >&2
		exit 1
	fi
	if [ -z "$old" ] || [ "$new" -gt "$old" ]; then
		changed="$changed $app"
	fi
done
if [ -z "$changed" ]; then
	echo "nothing to publish: the relay already has phone $phone_code and watch $watch_code"
	exit 0
fi

install -d -m 0750 -o agentwatch -g agentwatch "$dir"

# APKs first, then the metadata: a client that reads a JSON always finds its
# immutable, fully written APK. A same-directory rename is atomic.
for app in $changed; do
	case "$app" in
	phone) file="$phone_file" ;;
	watch) file="$watch_file" ;;
	esac
	install -m 0640 -o agentwatch -g agentwatch "$remote_tmp/$file" "$dir/.$file.new"
	mv -f "$dir/.$file.new" "$dir/$file"
done
for app in $changed; do
	install -m 0640 -o agentwatch -g agentwatch "$remote_tmp/$app.json" "$dir/.$app.json.new"
	mv -f "$dir/.$app.json.new" "$dir/$app.json"
done

# Keep the previous builds for a week for downloads that started just before
# a publish; the relay never deletes anything itself. The APK each app's
# current metadata names is kept however old it is: it is still the build
# clients download when that app had no update this time.
current_apk() {
	local file="$dir/$1.json"
	[ -f "$file" ] || return 0
	sed -n 's/.*"apk"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$file" | head -n 1
}
phone_apk="$(current_apk phone || true)"
watch_apk="$(current_apk watch || true)"
find "$dir" -maxdepth 1 -type f \( -name 'phone-*.apk' -o -name 'watch-*.apk' \) \
	-mtime +7 ! -name "$phone_apk" ! -name "$watch_apk" -delete

echo "published$changed to $dir"
REMOTE
