# {{ $params.title }}

<p v-if="$params.subtitle" class="feature-tagline">{{ $params.subtitle }}</p>

<DeckViewer :deck="$params" />
