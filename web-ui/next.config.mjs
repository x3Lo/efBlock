/** @type {import('next').NextConfig} */
const nextConfig = {
  // Export statique : `next build` produit de simples fichiers HTML/JS/CSS dans out/.
  // Le binaire Go les embarque : aucun serveur Node n'est nécessaire sur la VM.
  output: "export",
  // /queries/ -> queries/index.html : le serveur de fichiers de Go sait les servir tels quels.
  trailingSlash: true,
  images: { unoptimized: true },
};

export default nextConfig;
