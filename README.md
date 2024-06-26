## Desgin

The tool takes one required argument: a directory that will be
treated as the root to recursively scan,
OR an `.eopkg` file to analyze.

```bash
em2f path/to/root
# or
em2f path/to/eopkg
```

To provide a convenient way to extract all information available of a package
into the
manifest, a proposed solution (implementation in progress) is as following:

- The only required argument is a `package.yml` file.

- A `pspec_x86_64.xml` file is expected to be found in the same directory of the
  `package.yml` file, otherwise its path can be provided on the command-line.

- Using `pspec_x86_64.xml` file, extract the (sub-)packages' names and download
  them from the binary repo.

  We should definitely provide an option to pass paths of the `.eopkg`s on the
  command-line as well, because we cannot cache those downloads (we can cache
  them, but we cannot verify the integrity because PSPEC manifests don't contain
  hashes). Perhaps we
  should just download them to a directory?

- (Optional, but recommended) verify the info between the binary package, PSPEC
  manifest, and the source recipe. For now, this
  includes the package's source name,
  version, release, and files.

- Now combine all the info we have and emit the stone manifests!

### Scanning

Now, consider what happens when we scan a file. We only take action is the file
is either a shared library `ET_DYN` or a relocatble binary `ET_EXEC`/`ET_REL`.
Note that a shared library can also be a relocatble binary (i.e. a shared
library can and usually does provide symbols while being linked with
symbols from other dynamic libraries as well).

#### Shared Library

Currently, we just add this file as a provider after determining its
architecture (either `x86_64` or `x86`). For example, if we encounter an
`x86_64` `libfoo.so` file, then we would add a provider entry
`soname(libfoo.so(x86_64))`.

Note that we add this libraries to a lookup table, so that when scanning the
libraries/symbols that an
[executable](#executable) is linked to, we know we can skip some libraries
because they're provided alongside the executable in the same package.

A future version is highly likely to incorporate ABI scanning as well.

#### Relocatble Binary

We simply iterate through the `DT_NEEDED` section, and then add a depend entry
that is formated exactly like a provider entry in the [Shared
Library](#shared-library) section.
