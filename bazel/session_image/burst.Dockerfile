# The burst image: the pinned session image plus the PostgreSQL csf serve
# needs inside a cloud job, where there is no Docker to provision its own
# (services/cloud/burst.sh starts it). PostgreSQL 18 matches the version CSF's
# own database runs (services/database). The PGDG signing key is pinned by
# checksum; its fingerprint is B97B 0AFC AA1A 47F0 44F2 44A0 7FCC 7D46 ACCC 4CF8.
#
# Build with bazel/session_image/build_burst.sh, which passes SESSION_IMAGE
# from bazel/session_image.txt.
ARG SESSION_IMAGE

FROM ${SESSION_IMAGE}
USER root
ADD --checksum=sha256:0144068502a1eddd2a0280ede10ef607d1ec592ce819940991203941564e8e76 \
    https://www.postgresql.org/media/keys/ACCC4CF8.asc \
    /usr/share/keyrings/pgdg.asc
# A remote ADD lands owner-only; apt verifies as its own unprivileged user.
RUN chmod 0644 /usr/share/keyrings/pgdg.asc \
    && . /etc/os-release \
    && printf 'deb [signed-by=/usr/share/keyrings/pgdg.asc] https://apt.postgresql.org/pub/repos/apt %s-pgdg main\n' "$VERSION_CODENAME" > /etc/apt/sources.list.d/pgdg.list \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends postgresql-18 \
    && rm -rf /var/lib/apt/lists/* \
    && /usr/lib/postgresql/18/bin/postgres --version
USER 1000:1000
