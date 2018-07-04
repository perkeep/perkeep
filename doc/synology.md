# Perkeep on Synology appliances

## Installation

To facilitate running Perkeep on Synology appliances, we try to provide packages (`.spk` files) that can be readily installed through the **Package Center**. They have been built for **DSM 6.2** but they should at least work for **DSM 6.1** as well. For now the packages have not been published in the Synology Package Center "market", so you need to download them manually among the ones we host:

* [Perkeep-6281-latest.spk](https://storage.googleapis.com/perkeep-release/synology/Perkeep-armv5-b5c76a70e8c8d0b158a3dd19c261c0b2e62cd693.spk)
* [Perkeep-x64-latest.spk](https://storage.googleapis.com/perkeep-release/synology/Perkeep-x86_64-b5c76a70e8c8d0b158a3dd19c261c0b2e62cd693.spk)

Before installing, as we use the admin user home directory to store data, you need to make sure user homes are enabled. To do that: go to **Control Panel** -> **User** -> **Advanced** tab -> **User Home**, and tick **Enable user home service**.

As you have downloaded the package yourself, it has to be installed manually as well, which means clicking on the **Manual Install** button in the Package Center. Then follow the installation instruction.

After the installation, the Perkeep server (perkeepd) can be started and stopped from the package page in the **Package Center**.

## Configuration

As a Synology comes with an HTTP server (nginx) already listening on standard ports (80 and 443), making Perkeep accessible on these ports requires configuring nginx accordingly, which we do not do for now. One of the simplest ways to do so, is to leave Perkeep listening on HTTP on a non-privileged port (like 3179, in the default configuration), and to add a reverse proxy to the nginx configuration: go to **Control Panel** -> **Application Portal** -> **Reverse Proxy** tab. For example, if your NAS domain is **example.com**, and you control the subdomains for it, you can make the source be **perkeep.example.com** (HTTPS, port 443), and the destination be **localhost** (HTTP, port 3179).

For the same reason (not supported by default nginx configuration), websockets will not be working in Perkeep.
All of the nginx configuration is in `/etc/nginx`, but it is out of the scope of this document to explain it.

The Perkeep configuration file is located at `/var/services/homes/admin/.config/perkeep/server-config.json`. You can use the **File Station** to download it, edit it, and upload it again to suit your needs. Or you can login with ssh and then use a console text editor to modify it.

The data (blobs and index) are stored by default at `/var/services/homes/admin/var/perkeep`.

The logs are at `/var/services/homes/admin/var/perkeep.log`.

The script controlling the service is installed at `/var/packages/Perkeep/scripts/start-stop-status`. For easier debugging, you can use it to manually stop and start the server.
