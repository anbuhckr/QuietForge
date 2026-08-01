export function viewArtifact(artifact) {
    if (window.viewArtifact) {
        window.viewArtifact(artifact);
    } else {
        console.warn("viewArtifact is not defined on window.");
    }
}
