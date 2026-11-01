# Cosmics tool image.
#
# kalilinux/kali-rolling ships apt and nothing else — none of nmap, nikto,
# sqlmap, hydra, nuclei, ffuf, subfinder, httpx or whatweb are present. Every
# tool the registry references must be installed here.
#
# Build:  docker build -t cosmics/kali-tools:latest -f docker/kali-tools.Dockerfile docker
# Verify: docker run --rm cosmics/kali-tools:latest /usr/local/bin/cosmics-verify
#
# Tool versions are pinned because the upstream asset filename embeds the version;
# a floating tag would make the build non-reproducible.

FROM kalilinux/kali-rolling:latest

ARG NUCLEI_VERSION=3.11.1
ARG HTTPX_VERSION=1.12.0
ARG SUBFINDER_VERSION=2.17.0
ARG FFUF_VERSION=2.3.0

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      dnsutils \
      git \
      jq \
      nmap \
      nikto \
      sqlmap \
      hydra \
      whatweb \
      gobuster \
      netcat-traditional \
      dnsrecon \
      whois \
      openssl \
      unzip \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL -o /tmp/nuclei.zip \
      "https://github.com/projectdiscovery/nuclei/releases/download/v${NUCLEI_VERSION}/nuclei_${NUCLEI_VERSION}_linux_amd64.zip" \
 && unzip -q /tmp/nuclei.zip -d /usr/local/bin nuclei \
 && chmod +x /usr/local/bin/nuclei \
 && rm -f /tmp/nuclei.zip

RUN curl -fsSL -o /tmp/httpx.zip \
      "https://github.com/projectdiscovery/httpx/releases/download/v${HTTPX_VERSION}/httpx_${HTTPX_VERSION}_linux_amd64.zip" \
 && unzip -q /tmp/httpx.zip -d /usr/local/bin httpx \
 && chmod +x /usr/local/bin/httpx \
 && rm -f /tmp/httpx.zip

RUN curl -fsSL -o /tmp/subfinder.zip \
      "https://github.com/projectdiscovery/subfinder/releases/download/v${SUBFINDER_VERSION}/subfinder_${SUBFINDER_VERSION}_linux_amd64.zip" \
 && unzip -q /tmp/subfinder.zip -d /usr/local/bin subfinder \
 && chmod +x /usr/local/bin/subfinder \
 && rm -f /tmp/subfinder.zip

RUN curl -fsSL -o /tmp/ffuf.tar.gz \
      "https://github.com/ffuf/ffuf/releases/download/v${FFUF_VERSION}/ffuf_${FFUF_VERSION}_linux_amd64.tar.gz" \
 && tar -xzf /tmp/ffuf.tar.gz -C /usr/local/bin ffuf \
 && chmod +x /usr/local/bin/ffuf \
 && rm -f /tmp/ffuf.tar.gz

COPY cosmics-verify.sh /usr/local/bin/cosmics-verify
RUN chmod +x /usr/local/bin/cosmics-verify \
 && /usr/local/bin/cosmics-verify

RUN useradd --create-home --shell /usr/sbin/nologin cosmics \
 && mkdir -p /work /out \
 && chown -R cosmics:cosmics /work /out

USER cosmics
WORKDIR /work

ENTRYPOINT []
CMD ["/usr/local/bin/cosmics-verify"]
