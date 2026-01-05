# Roadmap

Our primary goal is to enable `gopyodide` to serve as a robust runtime for
**embedded ETL steps**. We are working towards supporting the following "North
Star" workflow:

1. **Ingest**: DuckDB loads data from a remote source (e.g. Postgres, S3) and
   saves it as a Parquet file.
2. **Transform**: Pandas loads the Parquet file, performs complex
   transformations, and saves the output.
3. **Export**: DuckDB reads the transformed data and pushes it to a remote
   destination.

_Critical constraint_: All intermediate files should be stored in a directory
mounted from the host, allowing the Go host to manage lifecycle and cleanup.

To achieve this, the following features are planned:

- [ ] **Host Filesystem Mounting**: Robust support for mounting host directories
      into the V8 virtual filesystem so Python scripts can read/write persistent
      Parquet files.
- [ ] **Network Polyfills**: Enhanced support for `fetch` and potentially socket
      emulation (proxied through Go) to allow database connections and cloud
      storage uploads from within the WASM environment.
- [ ] **Efficient Data Hand-off**: Optimization of the shared memory path to
      ensure large datasets (Parquet files) can be processed with minimal
      overhead.
