<?php
/**
 * Plugin Name: Flipbook Embed
 * Description: Adds the [flipbook] shortcode for accessible, responsive flipbook embeds.
 * Version: 1.0.0
 * Requires at least: 6.0
 * Requires PHP: 7.4
 * License: MIT
 *
 * Usage: [flipbook url="https://reader.example/v/annual-report" aspect="16/9" page="1" toolbar="true" search="true" share="true" download="true" branding="true" mode="single"]
 */

if (!defined('ABSPATH')) {
    exit;
}

function flipbook_embed_bool($value, $default = true) {
    if ($value === null || $value === '') {
        return $default;
    }
    $parsed = filter_var($value, FILTER_VALIDATE_BOOLEAN, FILTER_NULL_ON_FAILURE);
    return $parsed === null ? $default : $parsed;
}

function flipbook_embed_dimension($value, $default, $minimum, $maximum) {
    $number = is_numeric($value) ? (int) $value : $default;
    return min(max($number, $minimum), $maximum);
}

function flipbook_embed_shortcode($attributes) {
    $atts = shortcode_atts(
        array(
            'url' => '',
            'title' => 'Interactive flipbook',
            'width' => 800,
            'height' => 600,
            'aspect' => '',
            'page' => 1,
            'toolbar' => 'true',
            'search' => 'true',
            'share' => 'true',
            'download' => 'true',
            'branding' => 'true',
            'mode' => 'single',
            'background' => '',
            'responsive' => 'true',
            'class' => '',
        ),
        $attributes,
        'flipbook'
    );

    $source = esc_url_raw(trim($atts['url']));
    $parts = wp_parse_url($source);
    if (!$parts || empty($parts['host']) || empty($parts['path']) || empty($parts['scheme']) || !in_array(strtolower($parts['scheme']), array('http', 'https'), true)) {
        return '<p class="flipbook-embed-error">' . esc_html__('A valid flipbook URL is required.', 'flipbook') . '</p>';
    }

    $path = $parts['path'];
    if (strpos($path, '/embed/') === false) {
        if (strpos($path, '/v/') === false) {
            return '<p class="flipbook-embed-error">' . esc_html__('Use a publication viewer or embed URL.', 'flipbook') . '</p>';
        }
        $path = preg_replace('#/v/#', '/embed/', $path, 1);
    }
    $source = $parts['scheme'] . '://' . $parts['host'] . (isset($parts['port']) ? ':' . $parts['port'] : '') . $path;

    $query = array();
    foreach (array('toolbar', 'search', 'share', 'download', 'branding') as $option) {
        $query[$option] = flipbook_embed_bool($atts[$option]) ? 'true' : 'false';
    }
    $page = flipbook_embed_dimension($atts['page'], 1, 1, 100000);
    if ($page > 1) {
        $query['page'] = $page;
    }
    $mode = sanitize_key($atts['mode']);
    if (!in_array($mode, array('single', 'double', 'continuous', 'text'), true)) {
        $mode = 'single';
    }
    if ($mode !== 'single') {
        $query['mode'] = $mode;
    }
    $background = sanitize_hex_color($atts['background']);
    if ($background) {
        $query['background'] = $background;
    }

    $responsive = flipbook_embed_bool($atts['responsive']);
    $width = flipbook_embed_dimension($atts['width'], 800, 200, 3840);
    $height = flipbook_embed_dimension($atts['height'], 600, 150, 2160);
    $aspect = trim($atts['aspect']);
    if ($aspect && !preg_match('/^\d+(\.\d+)?\/\d+(\.\d+)?$/', $aspect)) {
        $aspect = '';
    }
    if ($aspect) {
        $query['aspect'] = $aspect;
        $responsive = true;
    }
    if (!$responsive) {
        $query['responsive'] = 'false';
        $query['width'] = $width;
        $query['height'] = $height;
    }

    $source = add_query_arg($query, $source);
    $title = sanitize_text_field($atts['title']);
    $classes = array('flipbook-embed');
    foreach (explode(' ', $atts['class']) as $class) {
        $class = sanitize_html_class($class);
        if ($class) {
            $classes[] = $class;
        }
    }
    $class_attribute = implode(' ', array_unique($classes));

    if ($responsive) {
        if (!$aspect) {
            $aspect = $width . '/' . $height;
        }
        return '<div class="' . esc_attr($class_attribute) . '" style="position:relative;width:100%;aspect-ratio:' . esc_attr($aspect) . '">' .
            '<iframe src="' . esc_url($source) . '" title="' . esc_attr($title) . '" style="position:absolute;inset:0;width:100%;height:100%;border:0" loading="lazy" allowfullscreen referrerpolicy="strict-origin-when-cross-origin"></iframe>' .
            '</div>';
    }

    return '<iframe class="' . esc_attr($class_attribute) . '" src="' . esc_url($source) . '" title="' . esc_attr($title) . '" width="' . esc_attr($width) . '" height="' . esc_attr($height) . '" style="border:0;max-width:100%" loading="lazy" allowfullscreen referrerpolicy="strict-origin-when-cross-origin"></iframe>';
}
add_shortcode('flipbook', 'flipbook_embed_shortcode');
