# Binaries are stripped (-s -w); suppress the empty debugsource subpackage.
%global debug_package %{nil}

Name:           sakms-node
Version:        %{version}
Release:        1%{?dist}
Summary:        sakms worker node daemon for GPU-accelerated media processing
License:        MIT
URL:            https://github.com/curtiswtaylorjr/sakms
Source0:        sakms-%{version}.tar.gz

# sakms-node and sakms-node-tray are pure Go (CGO_ENABLED=0);
# no GL or C build requirements needed.
BuildRequires:  golang >= 1.22
# Provides %{_unitdir} for the systemd unit file in %files below — COPR's
# minimal mock buildroot doesn't pull this in unless explicitly required.
BuildRequires:  systemd-rpm-macros

Requires(pre): shadow-utils
Requires(post): systemd
Requires(preun): systemd
Requires(postun): systemd
Requires:       python3
Requires:       curl

%description
sakms-node is the worker node daemon for the sakms self-hosted media
library server. It pairs with the sakms server via a one-time pairing
code, then receives GPU-accelerated phash jobs over a secure connection.

Install sakms-node-tray (a separate optional subpackage) to get a system
tray icon that displays the node's current state and pairing code.

%package tray
Summary:        System tray companion for sakms-node
Requires:       sakms-node = %{version}-%{release}
Requires:       dbus
# Provides the base hicolor theme directory structure our brand icon installs
# into, and gtk-update-icon-cache's target dir; not pulled in transitively by
# dbus/sakms-node, so declare it explicitly.
Requires:       hicolor-icon-theme
# wl-copy (wayland) or xclip/xsel (X11) for clipboard support — optional
Recommends:     wl-clipboard
Recommends:     libnotify

%description tray
sakms-node-tray is a CGo-free system tray companion (StatusNotifierItem /
dbus) that reflects the worker node lifecycle as a coloured icon:
amber = pending pairing, green = connected, red = not running.
It displays the 6-character pairing code and supports one-click copy.

%prep
%autosetup -n sakms-%{version}

%build
export CGO_ENABLED=0
export GOFLAGS="-mod=vendor"

go build -trimpath -ldflags "-s -w" -o sakms-node     ./cmd/sakms-node/
go build -trimpath -ldflags "-s -w" -o sakms-node-tray ./cmd/sakms-node-tray/

%install
install -Dm755 sakms-node      %{buildroot}%{_bindir}/sakms-node
install -Dm755 sakms-node-tray %{buildroot}%{_bindir}/sakms-node-tray

install -Dm644 packaging/rpm/sakms-node.service \
    %{buildroot}%{_unitdir}/sakms-node.service

install -Dm644 packaging/rpm/sakms-node-tray.desktop \
    %{buildroot}%{_sysconfdir}/xdg/autostart/sakms-node-tray.desktop

# Brand icon for the tray launcher entry (Icon=sakms-node in the .desktop
# above resolves this by name via freedesktop icon-theme lookup). Copied
# straight from the frontend's single source of truth — no second checked-in
# copy — into the hicolor scalable/apps theme dir.
install -Dm644 frontend/public/favicon.svg \
    %{buildroot}%{_datadir}/icons/hicolor/scalable/apps/sakms-node.svg

install -Dm755 packaging/rpm/post-install.sh \
    %{buildroot}%{_datadir}/sakms-node/post-install.sh

# Phase 2 (OS-level namespace containment) activator. Root-run ONLY (0700
# root:root) — tighter than post-install.sh's 0755 because this helper reads
# mediaRoots (writable by the non-root daemon it contains) and writes a
# root-loaded systemd drop-in, so it must not be world-readable/executable.
# Deliberately NOT invoked from any scriptlet below (see %post) — Phase 2
# activation is a separate, explicit, manual operator action.
install -Dm700 packaging/rpm/apply-mediaroots.sh \
    %{buildroot}%{_libexecdir}/sakms-node/apply-mediaroots

install -dm700 %{buildroot}%{_sysconfdir}/sakms-node

%pre
# Security-hardening addendum: sakms-node runs as a dedicated, non-root
# system user (not User=root — see sakms-node.service) so a compromised or
# buggy daemon has only this user's own permissions, not full filesystem
# access. getent-guarded for idempotency on upgrade/reinstall, since a bare
# useradd fails (and this spec deliberately never swallows scriptlet
# failures) if the user already exists from a prior install.
getent passwd sakms-node >/dev/null || useradd -r -s /sbin/nologin sakms-node

%post
# NOTE: apply-mediaroots (Phase 2 OS-level namespace containment) is
# deliberately NOT invoked here or in %pre/%postun. Per Decision Driver 3 /
# Principle 3, auto-generating a mount-namespace drop-in and restarting the
# daemon inside a package transaction would silently change (and could break)
# an existing Phase-1-only install's sandbox on upgrade. Activation stays a
# separate, explicit, manual operator action: run
# %{_libexecdir}/sakms-node/apply-mediaroots after editing mediaRoots.
%systemd_post sakms-node.service
# Re-own the config directory on EVERY install/upgrade ($1 == 1 or 2), not
# just fresh installs -- this is what actually migrates an existing
# root-owned install (from before the security-hardening addendum, when
# the daemon ran as User=root) to the new non-root sakms-node user. Without
# this running unconditionally, an upgrading node's root-owned config.json
# becomes unreadable to the new non-root service user and the daemon fails
# to start (the exact config-ownership failure class this addendum's
# execution-model change was reviewed against). post-install.sh's own
# chown is fresh-install-only (it only runs there at all, per the $1 -eq 1
# guard below) and cannot cover this case by itself.
if [ -d %{_sysconfdir}/sakms-node ]; then
    chown -R sakms-node:sakms-node %{_sysconfdir}/sakms-node
fi
# Run the interactive config writer + service enabler only on fresh installs.
# No `|| true`: post-install.sh's own exit code must propagate so a genuine
# failure (e.g. no SAKMS_SERVER_URL in a non-interactive install) surfaces as
# a real %post/dnf failure, not a silently-swallowed success.
if [ $1 -eq 1 ]; then
    %{_datadir}/sakms-node/post-install.sh
fi

%preun
%systemd_preun sakms-node.service

%postun
%systemd_postun_with_restart sakms-node.service

%files
%license LICENSE
%doc README.md
%{_bindir}/sakms-node
%{_unitdir}/sakms-node.service
%{_datadir}/sakms-node/post-install.sh
%attr(0700,root,root) %{_libexecdir}/sakms-node/apply-mediaroots
%dir %attr(700,sakms-node,sakms-node) %{_sysconfdir}/sakms-node

%posttrans tray
# Refresh the icon-theme cache once per transaction so the freshly installed
# sakms-node.svg is picked up. Guarded with `|| :` so a missing
# gtk-update-icon-cache binary (plausible on a minimal/headless host, which
# this daemon's is commonly) doesn't fail the transaction.
gtk-update-icon-cache -q %{_datadir}/icons/hicolor &>/dev/null || :

%files tray
%{_bindir}/sakms-node-tray
%{_sysconfdir}/xdg/autostart/sakms-node-tray.desktop
%{_datadir}/icons/hicolor/scalable/apps/sakms-node.svg

%changelog
* %(date "+%a %b %d %Y") packager <packager@example.com> - %{version}-1
- Initial packaging
- Add apply-mediaroots (Phase 2 OS-level namespace containment activator) as a
  root-only helper under %{_libexecdir}/sakms-node; not auto-invoked on install
