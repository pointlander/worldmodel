# WorldModel

Symmetric compressors in Go. The encoder and the decoder share one model, and decompressing a blob restores the original file bytes.

The module is `github.com/pointlander/worldmodel` and requires Go 1.25. The `cell` command also requires `ffmpeg` and `ffprobe`.

```
go test ./...
```

## cell

`cell` turns a video of dividing cells into one CSV row per frame, then compresses that table. Each row is the frame time followed by the red, green, and blue sample of every pixel. The time is the frame index divided by the rate reported by the video.

```
go run ./cell
go run ./cell -size 64 -video cell/01_video.webm
go run ./cell predict cell/cell.bin cell/future.gif
```

```
usage: cell [-size 32|64|full|WxH] [-video FILE] [file]
       cell predict [file] [gif]
```

The default picture is 32×32. `-size full` keeps the video's own frame size. `-video` defaults to `DNA cell death and division-L.mp4` in the working directory or in `cell/`. A 32×32 conversion is named `cell.bin`; any other size is named `cell{w}x{h}.bin`, next to the video unless an output path is given. The tracked `cell/cell.csv` is the canonical table the tests compress.

`predict` reads that blob, appends 33 forecast frames, and writes a GIF. A Markov chain draws the next law and motion, and each cell's elastic mesh advances under that draw.

The compressor predicts each frame after the first. A block warp cuts every block into two triangles and may split a triangle at the midpoints of its edges. A mesh law moves the grid by one velocity per block. In the second half of an even-length clip, a frame may repeat its paired earlier frame. The canonical 32×32 clip compresses to 22995 bytes.

## iris

The Fisher iris file. The three classes are fifty-row blocks, so the class is the row index. Each measurement is an integer tenth of a centimeter inside a per-class span, and a row is one mixed-radix digit in the product of those spans. The packed form is 298 bytes.

`Search` also builds one Turing-machine program for the file. Lines are searched, then paired, until a single program remains.

## bcwd

The Wisconsin diagnostic breast cancer file. Each of the thirty features is a decimal with at most four significant digits, coded as an integer in that column's mantissa range. Identifiers carry a two-bit width tag, and the diagnosis is one bit.

## secom

The SECOM semiconductor files. The matrix is 1567 rows by 590 features, with `NaN` for a missing cell. A value is an integer at that column's fixed decimal scale, Rice-coded from the column minimum, and identical missing-cell patterns are stored once. The labels file is a pass/fail bit and a Rice-coded run of timestamps.

## tm

Arbitrary bytes as one Turing-machine program. The search applies a Burrows-Wheeler transform and move-to-front, then picks the shortest program under a fixed instruction table:

| Instruction | Form |
|---|---|
| emit | `01 bb` |
| lit | `02 nn bb...` |
| rep | `03 bb nn` |
| copy | `04 dist u16 nn` |
| far | `05 dist u32 nn` |

`copy` and `far` append bytes from earlier on the tape. `rep` repeats one byte. `lit` and `emit` write raw bytes. The field tables stay in the decoder.

## Number-theoretic transform

The root command prints the number-theoretic transform of a file and checks the inverse.

```
go run . [file]
```

The field is Z/998244353Z. Each input byte is one residue, and the input is zero-padded to the next power of two up to 2^23. The default file is `iris/iris.data`.

## License

BSD. See [LICENSE](LICENSE).
