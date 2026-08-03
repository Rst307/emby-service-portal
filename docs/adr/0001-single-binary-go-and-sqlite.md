# Use a single Go binary with SQLite

The application manages one Emby server and prioritizes low memory use and simple deployment. It will use a server-rendered Go application with SQLite in WAL mode, rather than separate frontend, cache, queue, and database services; the persistence seam must allow a future PostgreSQL adapter if deployment scale requires it.
