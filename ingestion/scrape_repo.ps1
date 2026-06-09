$OutputFile = "codebase_snapshot.md"
$Exclusions = @("*.exe", "*.png", "*.jpg", "*.ico", "vendor", ".git", ".idea", "node_modules")

Remove-Item $OutputFile -ErrorAction Ignore

Add-Content $OutputFile "# Repository Architecture Snapshot"
Add-Content $OutputFile "Generated on: $(Get-Date)"
Add-Content $OutputFile "## Directory Tree"
Add-Content $OutputFile '```'
Get-ChildItem -Recurse -Directory | 
    Where-Object { $_.FullName -notmatch "(\.git|vendor)" } | 
    Select-Object FullName | 
    Out-String | Add-Content $OutputFile
Add-Content $OutputFile '```'
Add-Content $OutputFile "---"

Get-ChildItem -Recurse -File | Where-Object {
    $item = $_
    $matchedExclusion = $false
    foreach ($exclude in $Exclusions) {
        if ($item.FullName -like "*$exclude*" -or $item.Name -like $exclude) {
            $matchedExclusion = $true
            break
        }
    }
    $validExtension = $item.Extension -match "\.(go|mod|sum|json|yaml|yml|tmpl|sh)$"
    return ($validExtension -and -not $matchedExclusion)
} | ForEach-Object {
    $RelativePath = Resolve-Path -Relative $_.FullName
    $Ext = $_.Extension.Replace(".", "")
    Add-Content $OutputFile "## File: $RelativePath"
    Add-Content $OutputFile ('```' + $Ext)
    Get-Content $_.FullName | Add-Content $OutputFile
    Add-Content $OutputFile '```'
    Add-Content $OutputFile ""
}

Write-Host "Scraped manifest generated successfully at: $OutputFile" -ForegroundColor Green
