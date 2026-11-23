![FakeScript Banner](./images/banner.png)

# <img src="images/fakescript-logo.svg" height="32px" width="32px"> FakeScript (`.fks`) v3.1

An Ahead-of-Time (AOT) compiled programming language with **strictly zero punctuation symbols** in source code. `fksc` compiles `.fks` files through a full **Lexer → AST → Type Checker → Go CodeGen** pipeline into standalone native binaries with zero external dependencies.

## Quick Install (`fksc` + `fkspm`)

Requires Go 1.22+. Run the visual installer in one command:

**Windows (PowerShell):**
```powershell
irm https://raw.githubusercontent.com/ChaiMaii/fakescript/main/install.ps1 | iex
```

**Linux / macOS:**
```bash
curl -fsSL https://raw.githubusercontent.com/ChaiMaii/fakescript/main/install.sh | sh
```

## Quick Start (Build from Source)

```powershell
# Build the compiler (Go standard library only)
go build -o fksc.exe fksc.go

# Run a program directly
.\fksc.exe run examples\showcase.fks

# Build a standalone native executable
.\fksc.exe build examples\script.fks

# Inspect the generated Go code
.\fksc.exe emit examples\script.fks

# Type-check without building
.\fksc.exe check examples\showcase.fks

# Run the full test suite
go test -v ./...
```

## Language Reference

<details>
<summary>Types & Literals</summary>

- `num` (`int64`): `0`, `42`, `negative 5`
- `text` (`string`): `say hello world end`, `say end` (empty)
- `flag` (`bool`): `true` / `yes`, `false` / `no`
- `list of <type>` (`[]T`): `list of 1 and 2 and 3 end`, `empty list of text`
- `action` (`func()`): lazy thunk created automatically when assigning a void call (`let greet be ln of print contains say hi end`)
</details>

<details>
<summary>Variables & Lists</summary>

```text
let score be 10
let name as text be say Ada end
set score to score plus 5

let items be list of say apple end and say banana end end
append say cherry end to items
set item 1 of items to say blueberry end
let first be item 0 of items
let count be length of items
```
</details>


<details>
<summary>Expressions & Precedence</summary>
    
- Grouping: `group <expr> done`
- Unary: `negative <num>`, `not <flag>`
- Multiplicative: `times`, `divided by` / `over`, `mod` / `remainder`
- Additive: `plus` (num or text), `minus`
- Comparisons: `is`, `is not`, `is under`, `is over`, `is at least`, `is at most`
- Logical: `and`, `or`
</details>

<details>
<summary>Control Flow</summary>
    
```text
if score is at least 90 do
    ln of print contains say A end
else if score is at least 75 do
    ln of print contains say B end
else do
    ln of print contains say C end
end

loop when i is under 5 do
    set i to i plus 1
end

loop from 0 to 10 as i do
    if i mod 2 is 0 do
        skip
    end
    if i is over 7 do
        stop
    end
    ln of print contains i
end

for each item in items do
    ln of print contains item
end
```
</details>


<details>
<summary>Functions & Input</summary>

```text
fn add with num a and num b returns num do
    give back a plus b
end

let total be exec add with 20 and 22

ask say Enter your name  end into username
ask num say Enter your age  end into age
```
*(Untyped parameters like `fn greet with user do` remain supported for v2.0 compatibility and default to `text`.)*
</details>

<details>
<summary>Standard Library ('import <mod>' or 'import <mod> as <alias>')</summary>

- **`print`**: `ln`, `raw` (variadic: `ln of print contains say score end and score`)
- **`math`**: `abs`, `min`, `max`, `pow`, `sqrt`, `rand`, `clamp`
- **`text`**: `upper`, `lower`, `trim`, `join`, `split`, `has`, `replace`, `fromnum`, `tonum`, `length`
- **`file`**: `read`, `write`, `append`, `exists`
- **`time`**: `now` (Unix ms), `sleep` (ms)
</details>

## License

Licensed under the [WTFPL](./LICENSE).
