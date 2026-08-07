// Package modelinstall implements the deliberately opt-in Bundle B installer.
package modelinstall

const CatalogVersion = "bundle-b-2026-08-07"
const PolicyVersion = "bundle-b-policy-1"

const PolicyText = "Uncanny Lab code is MIT. The MIT license does not cover third-party checkpoints. When enabled, this feature downloads the listed checkpoint files directly from their upstream sources and converts them locally. The pinned MIT conversion source code listed below is included in the image and is not downloaded during installation. You are responsible for determining whether you have permission to download, convert, and use checkpoints. VQGAN and BigGAN checkpoint-specific terms are uncertain. Uncanny Lab does not redistribute source or converted checkpoints. Generated images may involve other rights."

type Source struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	LicenseNote string `json:"license_note"`
	Bytes       int64  `json:"bytes"`
}
type Repo struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	URL     string `json:"url"`
	Commit  string `json:"commit"`
	Tree    string `json:"tree"`
	Bundled bool   `json:"bundled"`
}
type Output struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Path  string `json:"path"`
}

var Sources = []Source{
	{"vgg19", "VGG19", "vgg19.pt", "https://download.pytorch.org/models/vgg19-dcbb9e9d.pth", "dcbb9e9dad569fff7a846263a77324fc34978fea2bfb039c012d710e1776ae44", "TorchVision terms apply", 574673361},
	{"clip-vit-b-32", "OpenAI CLIP ViT-B/32", "ViT-B-32.pt", "https://openaipublic.azureedge.net/clip/models/40d365715913c9da98579312b702a82c18be219cc2a73407c4526f58eba950af/ViT-B-32.pt", "40d365715913c9da98579312b702a82c18be219cc2a73407c4526f58eba950af", "OpenAI CLIP terms apply", 353976522},
	{"vqgan-checkpoint", "CompVis VQGAN checkpoint", "vqgan-imagenet-f16-16384.ckpt", "https://heibox.uni-heidelberg.de/d/a7530b09fed84f80a887/files/?p=%2Fckpts%2Flast.ckpt&dl=1", "845a68805098cb666420d5db93df53f3a3b6dd443e6dd85c05759c5b998cd663", "Checkpoint-specific terms are uncertain", 980092370},
	{"vqgan-config", "CompVis VQGAN config", "vqgan-imagenet-f16-16384.yaml", "https://heibox.uni-heidelberg.de/d/a7530b09fed84f80a887/files/?p=%2Fconfigs%2Fmodel.yaml&dl=1", "00e2c6189926f1d89ecfef73e9598db77981c1982f0555fbade963ffd16143c7", "CompVis source terms apply", 692},
	{"biggan-checkpoint", "BigGAN-deep-256 checkpoint", "biggan-deep-256.bin", "https://s3.amazonaws.com/models.huggingface.co/biggan/biggan-deep-256-pytorch_model.bin", "5900ef4065047e3aa0d1b66197b7f2664dceb59b27080e414ad57e203c485bc5", "Checkpoint-specific terms are uncertain", 234411737},
	{"biggan-config", "BigGAN config", "biggan-deep-256-config.json", "https://s3.amazonaws.com/models.huggingface.co/biggan/biggan-deep-256-config.json", "edd106f65ff28ee1638491a978cfa6bc50dfa0344aab70749a7b8eb08bbec677", "Hugging Face source terms apply", 715},
}
var Repos = []Repo{
	{"taming-transformers", "CompVis/taming-transformers", "https://github.com/CompVis/taming-transformers.git", "3ba01b241669f5ade541ce990f7650a3b8f65318", "cb6fd749bbad796fdef2dc7e9ad9f680c8ca462c", true},
	{"pytorch-pretrained-biggan", "huggingface/pytorch-pretrained-BigGAN", "https://github.com/huggingface/pytorch-pretrained-BigGAN.git", "1e18aed2dff75db51428f13b940c38b923eb4a3d", "f9c893ec07560e132e24aad0bf1040394892ced1", true},
}
var Outputs = []Output{{"vgg19-imagenet", "VGG19 classifier", "bundle-b/classifiers/vgg19.pt"}, {"clip-vit-b-32", "CLIP ViT-B/32", "bundle-b/clip/vit-b-32.pt"}, {"vqgan-decoder", "VQGAN decoder", "bundle-b/vqgan/decoder.pt"}, {"vqgan-codebook", "VQGAN codebook", "bundle-b/vqgan/codebook.pt"}, {"biggan-generator", "BigGAN generator", "bundle-b/biggan/generator.pt"}}
