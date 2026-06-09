#!/bin/sh
set -eu

# Edit SOURCE_HOSTS before running. Include the source LAN IP used by target computers.
SOURCE_HOSTS=${SOURCE_HOSTS:-192.168.1.10,localhost,127.0.0.1}

mkdir -p certs

./onesync-cert -hosts "$SOURCE_HOSTS" -cert certs/source.crt -key certs/source.key

printf '\nGenerated certs/source.crt and certs/source.key\n'
printf 'The generated synchronization link will carry certs/source.crt to the target.\n'
printf 'Do not copy certs/source.key.\n'
