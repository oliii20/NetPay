# Matching-time figure: version B

This folder is self-contained. It reproduces the distribution-oriented matching-time
figure from 20 samples: five independent repeats at 4, 8, 16, and 24 shards.

## Files

- `plot_matching_time_version_b.py`: standalone plotting script.
- `matching_time_data.csv`: the 20 raw measurements used by the script.
- `requirements.txt`: Python dependency specification.

## Reproduce

```bash
python3 -m pip install -r requirements.txt
python3 plot_matching_time_version_b.py
```

The script writes `matching_time_version_b.pdf`, `.png`, and `.svg` in this folder.
Use the PDF or SVG version in the paper to preserve vector text and lines.
