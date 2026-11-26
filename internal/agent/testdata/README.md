# internal/agent testdata

## text_document.pdf

A single-page, text-rich PDF (no embedded images) used by the inline-PDF
detection tests as the negative case — `isImageBasedPDF` must classify it as
text-based. It carries well over `minPDFCharsPerPage` characters of selectable
text so the per-page text-density check treats the page as text-rich.

Committed as a binary because a valid text PDF with a correct cross-reference
table can't be hand-authored reliably in-test (unlike the image PDFs, which the
tests generate with `pdfcpu`). To regenerate an equivalent fixture on macOS:

```sh
printf 'Quarterly Financial Report\nThis document contains a substantial amount of selectable text so that the\nextractor recovers well over fifty characters per page.\n' > sample.txt
cupsfilter sample.txt > text_document.pdf
```

Any generator that emits a single-page PDF with a few sentences of extractable
text works; the tests only depend on it being parseable and text-rich.
