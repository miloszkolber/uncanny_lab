# Model licensing review

Reviewed on 2026-08-07 for the current use of Uncanny Lab as a private, loopback-only local art and research tool. This is a technical provenance review, not legal advice.

## VQGAN ImageNet f16/16384

- The CompVis `taming-transformers` source code is MIT licensed. Its `License.txt` permits use, modification, publication, distribution, sublicensing, and sale of the software.
- The selected checkpoint is hosted separately by Heidelberg University and linked from the CompVis project. No checkpoint-specific license or terms are included with that download.
- CompVis issue [#209](https://github.com/CompVis/taming-transformers/issues/209), opened by a user in 2023 and still unanswered at review time, specifically asks the authors to license this checkpoint. It records the same unresolved concern but is not an authoritative interpretation from the model authors.
- The model was trained on ImageNet. ImageNet access and image rights are separate from the code license and may impose additional restrictions or uncertainty.

**Conclusion:** Retention for private local experimentation is an internal risk-containment decision, not licensing clearance. Do not redistribute the source checkpoint or converted weights. Obtain explicit legal clearance or replace it with a checkpoint carrying clear terms before further use, especially in a commercial product, customer workflow, public model service, or distributed application.

## BigGAN-deep-256

- Hugging Face's `pytorch-pretrained-BigGAN` implementation and conversion code are MIT licensed.
- The implementation is an op-for-op PyTorch reimplementation. Its weights were separately converted from DeepMind's TensorFlow Hub BigGAN-deep-256 generator trained on ImageNet.
- The official TensorFlow Hub documentation says the pretrained generator was released to permit verification of the research. The TF Hub documentation repository is Apache-2.0, but no authoritative model-asset license was found that clearly grants Apache-2.0 terms to the hosted BigGAN weights themselves.
- Secondary projects describe the DeepMind model as Apache-2.0, but that is not a substitute for an explicit license from the model publisher.
- ImageNet data and image rights remain separate considerations.

**Conclusion:** Retention for private local experimentation is an internal risk-containment decision, not licensing clearance. Do not redistribute the source checkpoint or converted generator. Seek confirmation from DeepMind/Google or legal counsel before further use, especially commercial deployment, public hosted inference, or distribution.

## Operational policy

- Keep the service bound to loopback and do not expose these model-backed engines as a public service.
- Keep source checkpoints and converted weights out of Git and application images.
- Preserve source URLs, hashes, conversion commits, and the generated provenance report under `/data/models/bundle-b/provenance`.
- Generated images can still implicate copyright, trademark, privacy, publicity, or dataset-related rights. Review intended publications independently.
- Revisit this review if the service use changes or the model publishers add checkpoint-specific terms.
