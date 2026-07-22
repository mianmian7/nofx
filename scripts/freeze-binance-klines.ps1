param(
    [Parameter(Mandatory = $true)]
    [string[]]$Symbols,
    [Parameter(Mandatory = $true)]
    [string]$Timeframe,
    [Parameter(Mandatory = $true)]
    [datetime]$Start,
    [Parameter(Mandatory = $true)]
    [datetime]$End,
    [Parameter(Mandatory = $true)]
    [string]$OutputDir
)

$ErrorActionPreference = 'Stop'
$startUtc = $Start.ToUniversalTime()
$endUtc = $End.ToUniversalTime()
if ($endUtc -le $startUtc) {
    throw 'End must be after Start'
}

New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null
$epoch = [datetime]::UnixEpoch
$startMs = [int64]($startUtc - $epoch).TotalMilliseconds
$endMs = [int64]($endUtc - $epoch).TotalMilliseconds

foreach ($rawSymbol in $Symbols) {
    $symbol = $rawSymbol.Trim().ToUpperInvariant()
    $cursor = $startMs
    $candles = [System.Collections.Generic.List[object]]::new()

    while ($cursor -lt $endMs) {
        $query = [System.Web.HttpUtility]::ParseQueryString('')
        $query['symbol'] = $symbol
        $query['interval'] = $Timeframe
        $query['limit'] = '1500'
        $query['startTime'] = [string]$cursor
        $query['endTime'] = [string]$endMs
        $uri = 'https://fapi.binance.com/fapi/v1/klines?' + $query.ToString()
        $batch = Invoke-RestMethod -Uri $uri -Method Get -TimeoutSec 30
        if (-not $batch -or $batch.Count -eq 0) {
            break
        }

        foreach ($item in $batch) {
            $candles.Add([ordered]@{
                openTime = [int64]$item[0]
                open = [double]$item[1]
                high = [double]$item[2]
                low = [double]$item[3]
                close = [double]$item[4]
                volume = [double]$item[5]
                closeTime = [int64]$item[6]
                quoteVolume = [double]$item[7]
                trades = [int]$item[8]
                takerBuyBaseVolume = [double]$item[9]
                takerBuyQuoteVolume = [double]$item[10]
            })
        }

        $lastClose = [int64]$batch[$batch.Count - 1][6]
        if ($lastClose -lt $cursor) {
            throw "Pagination did not advance for $symbol"
        }
        $cursor = $lastClose + 1
        if ($batch.Count -lt 1500) {
            break
        }
    }

    $json = $candles | ConvertTo-Json -Depth 4 -Compress
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($json)
    $hash = [Convert]::ToHexString([System.Security.Cryptography.SHA256]::HashData($bytes)).ToLowerInvariant()
    $startLabel = $startUtc.ToString('yyyy-MM-dd')
    $endLabel = $endUtc.ToString('yyyy-MM-dd')
    $path = Join-Path $OutputDir "$symbol-$Timeframe-$startLabel-$endLabel-$($hash.Substring(0, 12)).json"
    [System.IO.File]::WriteAllBytes($path, $bytes)
    [pscustomobject]@{
        symbol = $symbol
        timeframe = $Timeframe
        candles = $candles.Count
        sha256 = $hash
        path = $path
    }
}
