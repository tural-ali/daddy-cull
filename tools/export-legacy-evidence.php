<?php
declare(strict_types=1);

if ($argc !== 2) {
    fwrite(STDERR, "usage: php export-legacy-evidence.php /state/cull.db\n");
    exit(2);
}

$db = new SQLite3($argv[1], SQLITE3_OPEN_READONLY);
$db->busyTimeout(3000);
$rows = $db->query(
    "SELECT i.path, i.size, i.mtime, h.psig, h.md5, i.phash, h.hashed_at
       FROM inventory i
       LEFT JOIN hashes h ON h.path = i.path AND h.size = i.size AND h.mtime = i.mtime
      WHERE h.psig IS NOT NULL OR h.md5 IS NOT NULL OR i.phash IS NOT NULL
      ORDER BY i.path"
);
while ($row = $rows->fetchArray(SQLITE3_ASSOC)) {
    echo json_encode([
        'path' => $row['path'],
        'source' => 'archive',
        'size' => (int) $row['size'],
        'mtime' => (int) $row['mtime'],
        'partialSignature' => $row['psig'],
        'fullHash' => $row['md5'],
        'perceptualHash' => $row['phash'],
        'hashedAt' => $row['hashed_at'],
    ], JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR), "\n";
}
