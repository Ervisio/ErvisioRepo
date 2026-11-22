# Repository file preview

Variant Studio: `001-file-preview`, B (Scheda file), decision `4c743d6b8888`.

The file keeps the existing breadcrumb, commit strip, Raw, download and action menu. Type and size sit to the right of the preview; at narrow widths the details flow beneath it. Reuse the repository’s `--bg`, `--sunk`, `--line`, `--ink2`, `--b-r` and `--font` tokens; do not add an accent or a second card. The centred alternative kept size in the toolbar; the edge-to-edge alternative gave video more space. Neither was selected.

[GitHub’s file views](https://docs.github.com/en/repositories/working-with-files/using-files/working-with-non-code-files#viewing-images) supplied the in-repository preview pattern. [MDN’s video reference](https://developer.mozilla.org/en-US/docs/Web/HTML/Reference/Elements/video) supplied the keyboard-accessible native playback controls. The React translation uses the real blob MIME type and size. It adds no custom transport controls, autoplay, fake poster or inferred caption track. Animated GIFs can be shown and hidden without loading or animating before interaction. A failed decode or fetch leaves the file details and download available. LFS pointers and unsupported binary formats keep their existing views.

## Anteprima file

La pagina mantiene i comandi esistenti. Il tipo e la dimensione sono accanto all’anteprima su desktop e sotto di essa su schermi stretti. Audio e video usano i controlli del browser senza riproduzione automatica; le GIF si mostrano su richiesta. Se l’anteprima non si carica, il file resta scaricabile.
