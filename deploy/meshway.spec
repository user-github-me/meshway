Name:           meshway
Version:        %{meshway_version}
Release:        %{meshway_release}%{?dist}
Summary:        Encrypted peer VPN, SOCKS and tunnel daemon
License:        Apache-2.0
Source0:        meshway
Source1:        meshway.service
Source2:        LICENSE
Source3:        THIRD_PARTY_NOTICES.txt
Requires(pre):  shadow-utils
Requires:       systemd, iproute, nftables
Requires(post): /usr/sbin/runuser
Recommends:     systemd-resolved
BuildArch:      %{meshway_arch}

%description
Open source encrypted peer daemon. Internet exits require explicit consent
and peer authorization. Installation never enables the service automatically.

%prep

%build

%install
install -D -m 0755 %{SOURCE0} %{buildroot}/usr/bin/meshway
install -D -m 0644 %{SOURCE1} %{buildroot}/usr/lib/systemd/system/meshway.service
sed -i 's|/usr/local/bin/meshway|/usr/bin/meshway|' %{buildroot}/usr/lib/systemd/system/meshway.service
install -D -m 0644 %{SOURCE2} %{buildroot}/usr/share/licenses/meshway/LICENSE
install -D -m 0644 %{SOURCE3} %{buildroot}/usr/share/licenses/meshway/THIRD_PARTY_NOTICES.txt

%pre
getent group meshway >/dev/null || groupadd -r meshway
getent passwd meshway >/dev/null || useradd -r -g meshway -d /var/lib/meshway -s /sbin/nologin meshway

%post
set -eu
install -d -m 0700 -o meshway -g meshway /var/lib/meshway
if [ ! -f /var/lib/meshway/config.json ]; then
  runuser -u meshway -- /usr/bin/meshway init --config /var/lib/meshway/config.json
fi
systemctl daemon-reload >/dev/null 2>&1 || :

%preun
if [ "$1" -eq 0 ]; then systemctl stop meshway.service >/dev/null 2>&1 || :; fi

%postun
systemctl daemon-reload >/dev/null 2>&1 || :

%files
/usr/bin/meshway
/usr/lib/systemd/system/meshway.service
%license /usr/share/licenses/meshway/LICENSE
%license /usr/share/licenses/meshway/THIRD_PARTY_NOTICES.txt
